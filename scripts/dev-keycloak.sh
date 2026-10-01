#!/usr/bin/env bash
# Runs a development Keycloak on http://localhost:8180 with the "ballet"
# realm imported from deploy/dev/keycloak/ballet-realm.json.
#
# Uses the Keycloak Java distribution (needs Java 21+), downloaded once into
# $BALLET_TOOLS_DIR (default ~/.cache/ballet). With Docker or Podman, use
# deploy/dev/compose.yaml instead. Data is in-memory: every start re-imports
# the realm from scratch.
set -euo pipefail

KC_VERSION=${KC_VERSION:-26.7.5}
TOOLS_DIR=${BALLET_TOOLS_DIR:-$HOME/.cache/ballet}
KC_HOME="$TOOLS_DIR/keycloak-$KC_VERSION"
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)

command -v java >/dev/null || { echo "dev-keycloak: Java 21+ is required" >&2; exit 1; }

if [[ ! -x "$KC_HOME/bin/kc.sh" ]]; then
  echo "dev-keycloak: downloading Keycloak $KC_VERSION into $TOOLS_DIR"
  mkdir -p "$TOOLS_DIR"
  curl -fsSL "https://github.com/keycloak/keycloak/releases/download/$KC_VERSION/keycloak-$KC_VERSION.tar.gz" \
    | tar -xz -C "$TOOLS_DIR"
fi

mkdir -p "$KC_HOME/data/import"
cp "$REPO_ROOT/deploy/dev/keycloak/ballet-realm.json" "$KC_HOME/data/import/"

export KC_BOOTSTRAP_ADMIN_USERNAME=admin KC_BOOTSTRAP_ADMIN_PASSWORD=admin
exec "$KC_HOME/bin/kc.sh" start-dev \
  --http-port 8180 \
  --hostname http://localhost:8180 \
  --db dev-mem \
  --import-realm
