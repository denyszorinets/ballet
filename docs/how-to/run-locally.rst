Run Ballet locally
==================

One command builds and runs the whole system on your machine:

.. code-block:: bash

   make run

Then open http://localhost:8080 and sign in as ``alice`` / ``alice``
(organization admin of the development realm, see
:doc:`configure-oidc`). Press :kbd:`Ctrl-C` to stop everything.

What it does
------------

``make run`` is ``make bundle`` followed by :repo:`scripts/run.sh`.

``make bundle``
   Installs the web dependencies if needed (``bun``), builds the web UI,
   copies it into ``core/internal/transport/webui/dist`` and builds every
   service into ``bin/``. Core is built with the ``bindata`` build tag,
   which **embeds the web UI in the binary** — one ``bin/core`` serves the
   API and the UI, like a release build. Without the tag (``make build``,
   ``go run``, tests) Core embeds nothing.

``scripts/run.sh``
   Starts, in order, waiting for each to answer:

   #. the development Keycloak on ``:8180`` (unless an OIDC issuer answers
      there already; the first start downloads it and needs Java 21+);
   #. Core on ``:8080`` (API, realtime, web UI), which writes the other
      services' tokens;
   #. Knowledge on ``:8081``;
   #. the LLM gateway on ``:8082``;
   #. a Runner named ``local``: the ``docker`` backend when ``docker info``
      succeeds, else the ``process`` backend (runs on this machine; as
      root it sets ``IS_SANDBOX=1`` for Claude Code, see
      :doc:`/architecture/decisions/0023-process-backend-for-development`).

   Every service's output is shown prefixed with its name and written to
   ``.run/logs/<service>.log``. If a service exits, the script stops the
   others.

State
-----

Databases, keys and service tokens live in ``.run/data`` (git-ignored)
and survive restarts. Delete ``.run/`` to start from scratch. Keycloak
keeps its data in memory: it re-imports the realm on every start.

Options
-------

Environment variables of ``scripts/run.sh``:

``BALLET_FAKE_LLM=1``
   Also run a fake Anthropic API on ``:9900`` and route the gateway to it
   (``BALLET_GATEWAY_ANTHROPIC_URL``): agent sessions and the planner run
   without a real API key (store any key, e.g. ``sk-fake``, as the
   project's Anthropic credential).

``BALLET_RUNNER_BACKEND``
   ``docker`` or ``process``, overriding the detection.

``BALLET_RUN_DIR``
   Directory for state and logs instead of ``.run``.

Any ``BALLET_*`` configuration variable
(:doc:`/reference/configuration`) still applies to its service, e.g.
``BALLET_CORE_OIDC_ISSUER_URL`` for another identity provider (it must
answer; Keycloak is then not started).

Working on the UI
-----------------

``make run`` serves the built UI; it does not reload on changes. For UI
work run the Vite dev server against it (:doc:`/development/web`):

.. code-block:: bash

   cd web && bun run dev     # http://localhost:5173, proxies to Core on :8080
