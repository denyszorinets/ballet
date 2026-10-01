#!/usr/bin/env bash
# Prints an access token for a development user (alice, bob or carol) from
# the development Keycloak, using the password grant of the ballet-web client.
#
#   curl -H "Authorization: Bearer $(scripts/dev-token.sh alice)" localhost:8080/api/v1/me
set -euo pipefail
user=${1:?usage: dev-token.sh <alice|bob|carol>}
curl -fsS -X POST "http://localhost:8180/realms/ballet/protocol/openid-connect/token" \
  -d grant_type=password -d client_id=ballet-web -d scope=openid \
  -d username="$user" -d password="$user" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])'
