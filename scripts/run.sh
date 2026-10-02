#!/usr/bin/env bash
# Runs Ballet locally in one terminal: the development Keycloak (unless one
# answers on :8180 already), Core with the embedded web UI, Knowledge, the
# LLM gateway and a Runner. Build first with `make bundle` (`make run` does
# both). Ctrl-C stops everything.
#
# State (databases, keys, service tokens) and logs live in $BALLET_RUN_DIR
# (default .run/ in the repository). Every BALLET_* variable set in the
# environment still configures its service; see docs/how-to/run-locally.rst.
#
#   BALLET_FAKE_LLM=1        route the gateway to a fake Anthropic API
#   BALLET_RUNNER_BACKEND    docker or process (default: docker when it answers)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin"
RUN_DIR=${BALLET_RUN_DIR:-$ROOT/.run}
ISSUER=${BALLET_CORE_OIDC_ISSUER_URL:-http://localhost:8180/realms/ballet}

for b in core knowledge gateway runner; do
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

if ! curl -fsS -o /dev/null "$ISSUER/.well-known/openid-configuration" 2>/dev/null; then
  if [[ -n ${BALLET_CORE_OIDC_ISSUER_URL:-} ]]; then
    echo "run: the OIDC issuer $ISSUER does not answer" >&2
    exit 1
  fi
  echo "run: starting the development Keycloak (first start downloads it)"
  start keycloak "$ROOT/scripts/dev-keycloak.sh"
  wait_for keycloak "$ISSUER/.well-known/openid-configuration" 300
fi

export BALLET_CORE_OIDC_ISSUER_URL=$ISSUER
export BALLET_CORE_RBAC_BOOTSTRAP_ORG_ADMINS=${BALLET_CORE_RBAC_BOOTSTRAP_ORG_ADMINS:-groups:ballet-admins}
start core "$BIN/core"
wait_for core http://localhost:8080/healthz 60
# Core writes the other services' tokens at start.
for t in knowledge gateway runner; do
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

if [[ -z ${BALLET_RUNNER_RUNNER_BACKEND:-} ]]; then
  if [[ -n ${BALLET_RUNNER_BACKEND:-} ]]; then
    BALLET_RUNNER_RUNNER_BACKEND=$BALLET_RUNNER_BACKEND
  elif command -v docker >/dev/null && docker info >/dev/null 2>&1; then
    BALLET_RUNNER_RUNNER_BACKEND=docker
  else
    BALLET_RUNNER_RUNNER_BACKEND=process
  fi
fi
export BALLET_RUNNER_RUNNER_BACKEND
export BALLET_RUNNER_RUNNER_NAME=${BALLET_RUNNER_RUNNER_NAME:-local}
# Claude Code refuses to skip permission prompts as root outside a sandbox
# (ADR-0023); runs of the process backend share this machine.
if [[ $BALLET_RUNNER_RUNNER_BACKEND == process && $(id -u) == 0 ]]; then
  export IS_SANDBOX=${IS_SANDBOX:-1}
fi
start runner "$BIN/runner"

cat <<EOF

  Ballet is running on http://localhost:8080
  Sign in as alice / alice (organization admin of the development realm).
  Runner backend: $BALLET_RUNNER_RUNNER_BACKEND. State and logs: $RUN_DIR
  Press Ctrl-C to stop.

EOF
wait -n "${pids[@]}" || true
echo "run: shutting down (logs: $RUN_DIR/logs)" >&2
