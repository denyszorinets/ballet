Web UI Development
==================

The web UI is a SvelteKit single-page application in ``web/``, written in
TypeScript and built with Bun (:doc:`/architecture/decisions/0002-go-and-svelte`).
It is compiled to static files (``web/build``) that Core serves.

Setup
-----

.. code-block:: bash

   cd web
   bun install

Run locally
-----------

.. code-block:: bash

   go run ./core/cmd/core          # Core on :8080
   cd web && bun run dev           # UI on http://localhost:5173

The dev server proxies ``/api`` (REST), ``/rpc`` (WebSocket),
``/config.json`` and ``/healthz`` to Core on ``127.0.0.1:8080``. To sign
in, run the development Keycloak and point Core at it
(:doc:`/how-to/configure-oidc`), then open http://localhost:5173 (use
``localhost``, not ``127.0.0.1``: it is a registered redirect URI).

Authentication
--------------

The SPA reads the issuer and client ID from Core's ``/config.json`` and
signs in with ``oidc-client-ts`` (authorization code flow with PKCE).
Tokens are kept in ``sessionStorage`` and renewed silently with the
refresh token. ``src/lib/session.ts`` creates the shared REST client
(``api``) and realtime client (``realtime``) once; pages obtain them with
``getSession()``. The realtime connection starts after sign-in; its state
is shown in the header.

Commands
--------

.. list-table::
   :header-rows: 1

   * - Command (in ``web/``)
     - Purpose
   * - ``bun run dev``
     - Development server with hot reload
   * - ``bun run build``
     - Static production build into ``web/build``
   * - ``bun run check``
     - Svelte and TypeScript type checking
   * - ``bun run lint`` / ``bun run format``
     - Prettier and ESLint
   * - ``bun run test``
     - Unit tests (Vitest), ``src/**/*.spec.ts``
   * - ``bun run test:e2e``
     - End-to-end tests (Playwright, Google Chrome), ``e2e/*.test.ts``;
       a fake OIDC provider (``e2e/fixtures/oidc.ts``) replaces Keycloak
   * - ``bun run test:e2e:live``
     - Live tests (``e2e-live/``) against a running Keycloak + Core + Vite
       dev stack; not run in CI

Conventions
-----------

- Svelte 5 runes mode everywhere.
- Server-side rendering is disabled (``src/routes/+layout.ts``); all
  routes fall back to ``index.html``.
- API clients live in ``src/lib/api/`` and take ``fetch`` as a parameter
  so they can be unit-tested without a server.
- The realtime client (``src/lib/realtime/``) implements
  :doc:`/reference/realtime-api`: MessagePack/JSON, authentication with
  refresh, heartbeats, reconnect with backoff, and stream subscriptions
  that resume after reconnecting (``onResync`` when the gap is too large).
  ``statusStore(client)`` exposes the connection status as a Svelte store.
- ``*.integration.spec.ts`` tests run the realtime client against the Go
  reference server ``kit/rpc/cmd/rpc-testserver``; they need the Go
  toolchain (``bun run test`` builds the server).
- End-to-end tests stub backend calls with ``page.route()`` unless they
  are explicitly integration tests.

End-to-end tests use Google Chrome. In the devcontainer it is installed
from Google's RPM (Playwright's installer does not support Fedora), and
Chrome runs with ``--no-sandbox`` because the container runs as root.
