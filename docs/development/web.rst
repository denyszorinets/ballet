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

``make run`` builds the UI into Core and runs the whole system on
http://localhost:8080 (:doc:`/how-to/run-locally`). For UI work, run the
Vite dev server for hot reload instead:

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

Pages
-----

.. list-table::
   :header-rows: 1

   * - Route
     - Purpose
   * - ``/``
     - Organizations the user can see; new organization (platform admins)
   * - ``/organizations/{organization}``
     - Organization: rename, projects, new project
   * - ``/projects/{project}``
     - Board: tickets by state, *Blocked* badge for ready tickets with
       unresolved blockers, quick ticket creation, epics and milestones;
       live through the ``project:<KEY>`` stream
   * - ``/items/{item}``
     - Item: edit (title, description, type, criteria, policy, epic,
       milestone), state transitions, dependencies, history; live through
       the ``item:<KEY>`` stream
   * - ``/organizations/{organization}/knowledge``
     - Knowledge space: entries filtered by kind and project, hybrid
       search (:doc:`/concepts/knowledge`)
   * - ``/organizations/{organization}/knowledge/new``
     - New entry; ``?item=WEB-12`` pre-links a tracker item and its project
       (the item page's *Add knowledge* link)
   * - ``/organizations/{organization}/knowledge/{entry}``
     - Entry: rendered Markdown, links to projects and items, edit
       (Markdown with preview), version history
   * - ``/skills?scope=…``
     - Skills of one scope (``platform``, ``organization:<key>``,
       ``project:<key>``); new skill
   * - ``/skills/{skill}``
     - Skill: draft editor (description, ``SKILL.md``, files), publish,
       unpublished changes and per-version diffs (``src/lib/diff.ts``)
   * - ``/projects/{project}/planner``
     - Planner chats of a project; start a chat
   * - ``/planner/{session}``
     - Chat with the planner: streamed answers (``planner.watch`` /
       ``planner.output``), tool calls shown collapsed, proposed
       changesets as cards to approve wholly or in part, Stop
   * - ``/projects/{project}/changesets``
     - The project's changesets by status; approve or reject; live through
       the ``project:<KEY>`` stream
   * - ``/projects/{project}/skills``
     - Effective skills of a project; pin a version, disable, re-enable
   * - ``/access``
     - Role bindings (users who manage access)

The item page also lists the knowledge entries linked to the item.
Markdown written by humans and agents is untrusted: it is rendered with
``marked`` and sanitized with DOMPurify (``src/lib/markdown.ts``) before
it reaches the page, so scripts, event handlers and ``javascript:`` URLs
are removed.

The changeset card (``src/lib/components/ChangesetCard.svelte``) keeps
the selection consistent with ``$ref`` dependencies
(``src/lib/changesets.ts``): selecting an operation selects the
operations it needs, deselecting one deselects those that need it — the
same rule Core enforces when applying.

Pages subscribe to their stream *before* loading the snapshot over REST,
refetch the changed item on each event, and reload on resync. Controls
are shown according to the user's bindings (``src/lib/permissions.svelte.ts``);
Core enforces authorization regardless.

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
       a fake OIDC provider (``e2e/fixtures/oidc.ts``) replaces Keycloak,
       a fake Core REST API (``e2e/fixtures/api.ts``) replaces Core, and
       ``e2e/fixtures/realtime.ts`` replaces the page's ``WebSocket`` with
       an in-page fake of the realtime API whose requests the test answers
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
