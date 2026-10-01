---
name: keycloak
description: >
  Run and use the development Keycloak (OIDC identity provider) for Ballet.
  Use when working on authentication, authorization/RBAC, role bindings,
  anything under /api that needs a logged-in user, the web UI login flow,
  or when you need an access token for curl/tests. Covers starting Keycloak,
  the "ballet" realm, test users and groups, getting tokens, and changing
  the realm.
---

# Development Keycloak

Ballet authenticates humans only through OIDC (ADR-0006). In development
the identity provider is Keycloak with the `ballet` realm defined in
`deploy/dev/keycloak/ballet-realm.json`.

## Start it

```bash
make dev-keycloak        # foreground; or run in background:
nohup scripts/dev-keycloak.sh > /tmp/keycloak.log 2>&1 &
```

- Runs the Keycloak **Java distribution** (Java 25 is in the devcontainer;
  the container cannot run Docker/Podman). First start downloads Keycloak
  into `~/.cache/ballet`.
- On hosts with Docker/Podman: `docker compose -f deploy/dev/compose.yaml up -d`.
- Ready in ~30 s. Check:
  `curl -sf http://localhost:8180/realms/ballet/.well-known/openid-configuration`
- In-memory database: every start re-imports the realm; manual changes in
  the admin console are lost on restart.
- Admin console: http://localhost:8180/admin (admin / admin).

Always use `localhost` (not `127.0.0.1`): the issuer is pinned to
`http://localhost:8180/realms/ballet` and tokens from another host name
fail issuer validation.

## Realm contents

| User | Password | Groups | Intended role |
|---|---|---|---|
| alice | alice | ballet-admins | organization admin |
| bob | bob | acme-devs | engineer for customer "acme" |
| carol | carol | acme-viewers | viewer for customer "acme" |

Client `ballet-web`: public, authorization code + PKCE (S256), redirect
URIs for `localhost:5173` (Vite dev), `:4173` (preview/e2e) and `:8080`
(Core). The direct password grant is enabled **for development only**
(token scripts and tests).

Access tokens carry `aud: "ballet"` (audience mapper), `groups` (plain
group names), `email`, `name`, `sub`. Ballet authorization maps claims —
usually `groups` — to role bindings; it never uses Keycloak roles.

## Get a token / call the API

```bash
TOKEN=$(scripts/dev-token.sh bob)
curl -H "Authorization: Bearer $TOKEN" localhost:8080/api/v1/me
```

Run Core against it (alice becomes org-admin through the bootstrap
binding; bob and carol need role bindings created by alice):

```bash
BALLET_CORE_OIDC_ISSUER_URL=http://localhost:8180/realms/ballet \
BALLET_CORE_RBAC_BOOTSTRAP_ORG_ADMINS=groups:ballet-admins \
  go run ./core/cmd/core
```

Typical setup for manual tests (as alice): create customer `acme`, then
bind `acme-devs` → `engineer` @ `customer:acme` and `acme-viewers` →
`viewer` @ `customer:acme` via `POST /api/v1/role-bindings`.

Decode a token's claims when debugging:

```bash
scripts/dev-token.sh alice | cut -d. -f2 | tr '_-' '/+' | base64 -d 2>/dev/null; echo
```

## Changing the realm

1. Edit `deploy/dev/keycloak/ballet-realm.json` (users, groups, client,
   mappers). Keep passwords equal to user names.
2. Restart Keycloak (it re-imports).
3. Verify with `scripts/dev-token.sh <user>` and the decoded claims.
4. Update the table above and `docs/how-to/configure-oidc.rst` if
   users, groups or claims change.

Do not export the realm from the admin console into the repo wholesale —
exports contain generated IDs, secrets and defaults; keep the file
minimal and hand-written.

## Tests

Go tests never need a running Keycloak: use `kit/auth/oidctest`
(`oidctest.NewIssuer(t)` serves discovery + JWKS and signs tokens with
`iss.Token(t, subject, "ballet", claims)`). Use real Keycloak for manual
end-to-end checks and browser login flows (see the frontend-debugging
skill).
