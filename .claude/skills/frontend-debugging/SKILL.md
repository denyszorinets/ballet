---
name: frontend-debugging
description: >
  Debug and verify Ballet's Svelte web UI with Playwright. Use when changing
  anything under web/, when a UI bug is reported, when you need to see what
  the UI actually renders, inspect console errors or network calls, or when
  adding/fixing end-to-end tests. Covers the Playwright MCP browser tools and
  the Playwright test suite in web/e2e.
---

# Frontend Debugging with Playwright

Never claim a UI change works without seeing it in a browser. Use the
Playwright MCP tools to look and interact; capture the verified behavior
as a Playwright test in `web/e2e/` so it stays verified.

## Environment facts

- Browser: Google Chrome at `/opt/google/chrome/chrome` (installed by the
  devcontainer `Containerfile`; Playwright's own installer does not
  support Fedora — do not run `npx playwright install`).
- The devcontainer runs as root, so Chrome needs `--no-sandbox`. The MCP
  server is configured with it in `.mcp.json`; `web/playwright.config.ts`
  adds it automatically when running as root.
- If the MCP browser fails with "Running as root without --no-sandbox",
  the MCP server was started with an old config: ask the human to
  reconnect it (`/mcp`), and meanwhile use the test suite.
- MCP artifacts (screenshots, snapshots) go to `.playwright-mcp/`
  (git-ignored).

## Start the app

The UI needs a login, so run Keycloak, Core and the Vite dev server in
the background (see the keycloak skill):

```bash
nohup scripts/dev-keycloak.sh > /tmp/keycloak.log 2>&1 &
BALLET_CORE_OIDC_ISSUER_URL=http://localhost:8180/realms/ballet \
BALLET_CORE_RBAC_BOOTSTRAP_PLATFORM_ADMINS=groups:ballet-admins \
  go run ./core/cmd/core &                  # Core on :8080
cd web && bun run dev --host localhost --port 5173 &
```

Open **http://localhost:5173** (not 127.0.0.1 — the redirect URI is
registered for localhost). Vite proxies `/api`, `/rpc` (WebSocket),
`/config.json` and `/healthz` to Core (see `web/vite.config.ts`). Sign
in as alice/alice (platform admin), bob/bob or carol/carol.

## Look at the UI (Playwright MCP)

1. `browser_navigate` to `http://127.0.0.1:5173/<route>`.
2. `browser_snapshot` — the accessibility tree; prefer it over
   screenshots for finding elements and reading text.
3. Interact with `browser_click`, `browser_type`, `browser_fill_form`,
   `browser_press_key`, using refs from the snapshot.
4. `browser_console_messages` (level `error`) after every navigation and
   interaction — a clean console is part of "works".
5. `browser_network_requests` to check API calls, status codes and
   payloads.
6. `browser_take_screenshot` only for visual/layout questions (theme,
   spacing, overflow); check both light and dark when styling changes
   (`browser_emulate_media`).
7. `browser_resize` to 390×844 to check the phone layout.

## Debugging loop

1. Reproduce in the browser via MCP; note the exact steps.
2. Read console errors and failing network requests first — most UI
   bugs show up there.
3. Write a failing Playwright test reproducing the bug (`web/e2e/`).
4. Fix, then re-run the test and re-check in the browser.

## Live tests when the MCP browser is unavailable

`web/e2e-live/` holds Playwright tests that drive the real dev stack,
including the Keycloak login form (`bun run test:e2e:live`). Use them to
verify behavior in a real browser when the MCP server cannot start
Chrome: they collect console errors, and `page.screenshot()` output in
`web/test-results/` can be inspected with the Read tool. Seed data
through the API first (`scripts/dev-token.sh alice` + curl).

## End-to-end tests

```bash
cd web
bun run test:e2e                      # builds, serves on :4173, runs tests
bun run test:e2e -- --debug           # step through (needs a display)
bun run test:e2e -- e2e/home.test.ts  # single file
```

Conventions:

- Tests live in `web/e2e/*.test.ts`; unit tests (Vitest) stay next to
  code in `src/**/*.spec.ts`.
- Stub backend calls with `page.route()` unless the test is explicitly an
  integration test against a running Core; tests must not depend on
  external services.
- Sign in with the fake OIDC provider: `await fakeOIDC(page)` from
  `e2e/fixtures/oidc.ts`, then click "Sign in" — it routes
  `/config.json`, discovery, authorize, token and logout.
- Select elements by role and accessible name
  (`getByRole('button', { name: 'Save' })`); use `data-testid` only for
  elements without a meaningful role.
- Assert with web-first assertions (`await expect(locator).toHaveText()`),
  never with sleeps.
- On failure, traces and screenshots are kept in `web/test-results/`;
  open a trace with `bunx playwright show-trace <trace.zip>`.

## Done means

- Behavior verified in the browser via MCP, console clean.
- A Playwright test covers the user-visible behavior.
- `make web-check` (lint, type check, unit tests) passes.
