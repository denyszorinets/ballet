Build an agent pool from a devcontainer
=======================================

Agents run coding-agent sessions in their own container, so the container
needs the project's toolchain
(:doc:`/architecture/decisions/0025-agent-fleet-runs-sessions-as-processes`).
Build it from the project's devcontainer: the **ballet-agent devcontainer
Feature** adds the agent, Claude Code and an unprivileged session user to
whatever the devcontainer already installs. Every container of that image
is an agent of the image's **pool**; Ballet sends a project's runs to the
agents of its pool.

1. Add the Feature
------------------

The Feature lives in :repo:`.devcontainer/ballet-agent`. Until it is
published to a registry, bundle the agent into it and copy it next to the
project's ``devcontainer.json`` (devcontainers only use local Features
inside the ``.devcontainer`` folder):

.. code-block:: bash

   make feature   # builds the agent for amd64 and arm64 into .devcontainer/ballet-agent/bin
   cp -r .devcontainer/ballet-agent /path/to/project/.devcontainer/

Then add it to the project's ``devcontainer.json`` — or to a second
configuration such as ``.devcontainer/pool/devcontainer.json``, so the
developers' own configuration stays as it is:

.. code-block:: json

   {
     "build": { "dockerfile": "../Dockerfile", "context": ".." },
     "features": {
       "../ballet-agent": { "pool": "web" }
     }
   }

Options:

``pool``
   The pool label (lowercase letters, digits, ``.``, ``_``, ``-``). Empty:
   the agents take any project's runs that names no pool.
``sessionUser``
   The OS user sessions run as, created if missing (default ``ballet``).
   Sessions then cannot read the agent's token. Empty: sessions run as
   root — only when the image keeps its tools where other users cannot
   reach them, like Ballet's own (:doc:`dogfood`).
``agentUrl``
   Where to download the agent when the Feature does not bundle it
   (``${ARCH}``: ``amd64`` or ``arm64``).
``installClaude``
   Install Claude Code (default ``true``; skipped when ``claude`` is in
   ``/usr/local/bin`` or ``/usr/bin`` already).

The Feature writes ``/etc/ballet/agent.toml`` (the pool label and the
session user; ``BALLET_AGENT_CONFIG`` points at it) — ``BALLET_AGENT_*``
variables still override it (:ref:`reference-agents-sessions`).

2. Build the image
------------------

With the devcontainer CLI (``npm install -g @devcontainers/cli``):

.. code-block:: bash

   devcontainer build --workspace-folder /path/to/project \
     --config /path/to/project/.devcontainer/pool/devcontainer.json --image-name acme-web-pool

:repo:`.devcontainer/agent-pool-example/devcontainer.json` is a minimal
example; ``make feature-test`` builds it and checks the result, as CI does.

3. Run the agents
-----------------

Run the image with the agent as its command, Core's URL and the agent
token Core writes (``data/service-tokens/agent.token``):

``/usr/local/bin/ballet-agent``
   The command. (``devcontainer up`` starts it through the Feature's
   entrypoint when ``BALLET_AGENT_CORE_URL`` is set.)
``BALLET_AGENT_CORE_URL``
   Core's base URL as reached from the container.
``BALLET_AGENT_CORE_TOKEN_FILE``
   The agent token, mounted read-only.

With the compose installation (:doc:`install-single-host`):

.. code-block:: bash

   BALLET_AGENT_IMAGE=acme-web-pool docker compose -f deploy/compose.yaml up -d agent

On Kubernetes, a Deployment of N replicas of the image, with the token in
a Secret, is the whole pool; scale it by changing N. Sessions reach the
LLM gateway, Knowledge and Core's tracker through the URLs Core hands
out (``agents.gateway_url``, ``agents.knowledge_mcp_url``,
``agents.tracker_mcp_url``, :doc:`/reference/configuration`).

4. Send the project's runs to the pool
--------------------------------------

In the project's settings, set **Agent pool** to the pool label
(``pool`` in :ref:`reference-rest-execution`). Its runs then go only to
agents labelled ``pool=<label>``; while none is connected, they wait in
the queue. Projects without a pool use any agent.
