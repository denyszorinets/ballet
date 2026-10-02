Install Ballet on one host
==========================

The compose installation runs Ballet's services as containers on one
Linux host with Docker Engine: Core (with the web UI), Knowledge, the LLM
gateway and a Runner whose agent sessions run as sibling containers.

Requirements
------------

- Linux with Docker Engine and the Compose plugin (``docker compose``).
  The services use the host network (``network_mode: host``); with
  Docker Desktop, enable host networking in its settings.
- Free ports 8080 (Core), 8081 (Knowledge), 8082 (gateway), and 8180 for
  the development Keycloak.
- An Anthropic API key for the projects, and a git token for their
  repositories.

Start
-----

.. code-block:: bash

   git clone https://github.com/denyszorinets/ballet.git && cd ballet
   docker compose -f deploy/compose.yaml --profile keycloak up -d --build
   docker build -t ballet-agent deploy/agent

The first command builds the images (``make images`` builds them too) and
starts the services; ``--profile keycloak`` adds the development Keycloak
with its demo users (:doc:`configure-oidc`). The second builds the image
agent sessions run in: git, Claude Code and common build tools — extend
it for your projects' toolchains.

Open http://localhost:8080 and sign in as ``alice`` / ``alice``. Then, as
in :doc:`run-locally`:

#. create a customer and a project;
#. store the customer's Anthropic key (customer page → *LLM credentials*);
#. in the project settings, set the repository, ``ballet-agent`` as the
   image, and a git token;
#. add tickets — or plan them with the planner — and move them to
   *Ready*.

Configuration
-------------

Every service reads its ``BALLET_*`` environment variables
(:doc:`/reference/configuration`); ``deploy/compose.yaml`` sets the
installation-specific ones and takes these from the environment or an
``.env`` file next to it:

``BALLET_OIDC_ISSUER_URL``
   Your identity provider's issuer (default: the development Keycloak).
   Register the ``ballet-web`` public client with
   ``http://<host>:8080/*`` as redirect URI.

``BALLET_ORG_ADMINS``
   Claim matchers that are organization admins, e.g.
   ``groups:ballet-admins``.

``BALLET_ANTHROPIC_URL``
   Anthropic API base URL (default ``https://api.anthropic.com``).

``BALLET_RUNNER_NAME``, ``BALLET_RUNNER_CAPACITY``
   The Runner's name and how many sessions it runs at once (default 2).

``BALLET_LOG_LEVEL``
   ``debug``, ``info`` (default), ``warn`` or ``error``.

State and backups
-----------------

All state lives in the ``ballet-data`` volume: Core's and Knowledge's
SQLite databases, the token signing keys, the key encrypting stored
credentials, and the service tokens. Back it up as a whole, with the
services stopped:

.. code-block:: bash

   docker compose -f deploy/compose.yaml stop
   docker run --rm -v ballet_ballet-data:/data -v "$PWD":/backup alpine \
     tar czf /backup/ballet-data.tgz -C /data .
   docker compose -f deploy/compose.yaml start

Upgrades
--------

.. code-block:: bash

   git pull
   docker compose -f deploy/compose.yaml up -d --build

Database migrations run when the services start. Pipelines in progress
continue (:ref:`reference-pipelines-recovery`).

Security notes
--------------

- With host networking the services listen on all interfaces: allow only
  port 8080 (or a TLS reverse proxy in front of it) through the host's
  firewall. The gateway and Knowledge are only needed by agent
  containers on the same host.
- The Runner uses the host's Docker socket to start agent containers;
  whoever controls the Runner controls the host's Docker engine.
- The development Keycloak and its demo users are for trying Ballet;
  use your own identity provider for real use.
