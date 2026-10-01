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

The dev server proxies ``/api`` (REST), ``/rpc`` (WebSocket) and
``/healthz`` to Core on ``127.0.0.1:8080``.

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
     - End-to-end tests (Playwright, Google Chrome), ``e2e/*.test.ts``

Conventions
-----------

- Svelte 5 runes mode everywhere.
- Server-side rendering is disabled (``src/routes/+layout.ts``); all
  routes fall back to ``index.html``.
- API clients live in ``src/lib/api/`` and take ``fetch`` as a parameter
  so they can be unit-tested without a server.
- End-to-end tests stub backend calls with ``page.route()`` unless they
  are explicitly integration tests.

End-to-end tests use Google Chrome. In the devcontainer it is installed
from Google's RPM (Playwright's installer does not support Fedora), and
Chrome runs with ``--no-sandbox`` because the container runs as root.
