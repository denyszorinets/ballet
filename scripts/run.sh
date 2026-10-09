#!/usr/bin/env bash
# Runs Ballet locally in one terminal: Core with the embedded web UI,
# Knowledge, the LLM gateway and an agent. Build first with `make bundle`
# (`make run` does both). Ctrl-C stops everything.
#
# By default there is no authentication: you are the local user, an
# platform admin, and Core listens on localhost only. BALLET_AUTH=oidc
# signs in through the development Keycloak instead (started unless one
# answers on :8180); BALLET_CORE_OIDC_ISSUER_URL uses another issuer.
#
# State (databases, keys, service tokens) and logs live in $BALLET_RUN_DIR
# (default .run/ in the repository). Every BALLET_* variable set in the
# environment still configures its service; see docs/how-to/run-locally.rst.
#
#   BALLET_AUTH=oidc         multiple users: sign in through the development Keycloak
#   BALLET_FAKE_LLM=1        route the gateway to a fake Anthropic API
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin"
RUN_DIR=${BALLET_RUN_DIR:-$ROOT/.run}
ISSUER=${BALLET_CORE_OIDC_ISSUER_URL:-}
if [[ -z $ISSUER && ${BALLET_AUTH:-} == oidc ]]; then
  ISSUER=http://localhost:8180/realms/ballet
fi

for b in core knowledge gateway agent; do
  [[ -x "$BIN/$b" ]] || { echo "run: $BIN/$b is missing: run 'make bundle' first" >&2; exit 1; }
done

mkdir -p "$RUN_DIR/logs"
cd "$RUN_DIR"

pids=()
stop() {
  trap - EXIT INT TERM
  echo "run: stopping"
  for ((i = ${#pids[@]} - 1; i >= 0; i--)); do kill "${pids[i]}" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap stop EXIT INT TERM

# start NAME COMMAND...: runs a service in the background; its output goes
# to logs/NAME.log and, prefixed with its name, to the terminal.
start() {
  local name=$1
  shift
  "$@" > >(tee -a "logs/$name.log" | sed -u "s/^/[$name] /") 2>&1 &
  pids+=($!)
}

# wait_for NAME URL SECONDS: waits until URL answers.
wait_for() {
  local name=$1 url=$2 secs=$3
  for ((i = 0; i < secs * 2; i++)); do
    curl -fsS -o /dev/null "$url" 2>/dev/null && return 0
    kill -0 "${pids[-1]}" 2>/dev/null || break # it exited
    sleep 0.5
  done
  echo "run: $name did not become ready at $url (see $RUN_DIR/logs/$name.log)" >&2
  exit 1
}

if [[ -n $ISSUER ]] && ! curl -fsS -o /dev/null "$ISSUER/.well-known/openid-configuration" 2>/dev/null; then
  if [[ -n ${BALLET_CORE_OIDC_ISSUER_URL:-} ]]; then
    echo "run: the OIDC issuer $ISSUER does not answer" >&2
    exit 1
  fi
  echo "run: starting the development Keycloak (first start downloads it)"
  start keycloak "$ROOT/scripts/dev-keycloak.sh"
  wait_for keycloak "$ISSUER/.well-known/openid-configuration" 300
fi

if [[ -n $ISSUER ]]; then
  export BALLET_CORE_OIDC_ISSUER_URL=$ISSUER
  export BALLET_CORE_RBAC_BOOTSTRAP_PLATFORM_ADMINS=${BALLET_CORE_RBAC_BOOTSTRAP_PLATFORM_ADMINS:-groups:ballet-admins}
  SIGN_IN="Sign in as alice / alice (platform admin of the development realm)."
else
  SIGN_IN="No sign-in: you are the local user (BALLET_AUTH=oidc for multiple users)."
fi
start core "$BIN/core"
wait_for core http://localhost:8080/healthz 60
# Core writes the other services' tokens at start.
for t in knowledge gateway agent; do
  i=0
  while [[ ! -s data/service-tokens/$t.token ]] && ((i++ < 20)); do sleep 0.5; done
done

start knowledge "$BIN/knowledge"
wait_for knowledge http://localhost:8081/healthz 30

if [[ ${BALLET_FAKE_LLM:-} == 1 ]]; then
  [[ -x "$BIN/fake-anthropic" ]] || { echo "run: $BIN/fake-anthropic is missing: run 'make bundle'" >&2; exit 1; }
  start fake-llm "$BIN/fake-anthropic"
  export BALLET_GATEWAY_ANTHROPIC_URL=http://127.0.0.1:9900
fi
start gateway "$BIN/gateway"
wait_for gateway http://localhost:8082/healthz 30

# Sessions run as processes on this machine (ADR-0025).
export BALLET_AGENT_AGENT_NAME=${BALLET_AGENT_AGENT_NAME:-local}
export BALLET_AGENT_AGENT_CAPACITY=${BALLET_AGENT_AGENT_CAPACITY:-2}
start agent "$BIN/agent"

cat <<EOF

  Ballet is running on http://localhost:8080
  $SIGN_IN
  Agent sessions run on this machine. State and logs: $RUN_DIR
  Press Ctrl-C to stop.

EOF
wait -n "${pids[@]}" || true
echo "run: shutting down (logs: $RUN_DIR/logs)" >&2
