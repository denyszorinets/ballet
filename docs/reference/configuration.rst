Configuration
=============

Every Ballet service (``core``, ``knowledge``, ``gateway``, ``runner``) is
configured the same way.

Sources and precedence
----------------------

From lowest to highest precedence:

#. built-in defaults;
#. a TOML file, given by ``-config <path>`` or the environment variable
   ``BALLET_<SERVICE>_CONFIG``;
#. environment variables ``BALLET_<SERVICE>_<SECTION>_<KEY>``.

``<SERVICE>`` is ``CORE``, ``KNOWLEDGE``, ``GATEWAY`` or ``RUNNER``. For
example, ``[server] addr`` of Core is ``BALLET_CORE_SERVER_ADDR``.

Configuration is read once at startup; changes require a restart. The
service refuses to start, with an error naming the problem, when:

- the file contains an unknown key (typos are not ignored);
- a value has the wrong type (``"soon"`` for a duration);
- a value is invalid (empty address, unknown log level).

Durations are strings such as ``"500ms"``, ``"10s"``, ``"1m30s"``.
Lists are TOML arrays in the file and comma-separated in environment
variables.

.. code-block:: toml

   # core.toml
   [server]
   addr = ":8080"
   shutdown_timeout = "10s"

   [log]
   level = "info"

.. code-block:: bash

   ./bin/core -config core.toml
   BALLET_CORE_LOG_LEVEL=debug ./bin/core -config core.toml

.. _reference-config-server:

``[server]``
------------

``addr``
~~~~~~~~

Address the HTTP server listens on.

:Type: string (``host:port``; empty host means all interfaces)
:Default: ``:8080`` (core), ``:8081`` (knowledge), ``:8082`` (gateway),
   ``:8083`` (runner)
:Environment: ``BALLET_<SERVICE>_SERVER_ADDR``

``shutdown_timeout``
~~~~~~~~~~~~~~~~~~~~

How long the service waits for in-flight requests after receiving
SIGINT or SIGTERM before closing connections.

:Type: duration, must be positive
:Default: ``"10s"``
:Environment: ``BALLET_<SERVICE>_SERVER_SHUTDOWN_TIMEOUT``

.. _reference-config-log:

``[log]``
---------

``level``
~~~~~~~~~

Minimum level of log records. Logs are JSON lines on standard output,
each with ``time``, ``level``, ``msg`` and ``service``.

:Type: string — ``debug``, ``info``, ``warn`` or ``error``
:Default: ``"info"``
:Environment: ``BALLET_<SERVICE>_LOG_LEVEL``

.. _reference-config-oidc:

``[oidc]`` (core)
-----------------

Human authentication (:doc:`/how-to/configure-oidc`).

``issuer_url``
~~~~~~~~~~~~~~

URL of the OIDC issuer; its discovery document must be at
``<issuer_url>/.well-known/openid-configuration``. Read at startup; the
service does not start if the issuer is unreachable.

:Type: string (URL)
:Required: yes
:Default: none
:Environment: ``BALLET_CORE_OIDC_ISSUER_URL``

``audience``
~~~~~~~~~~~~

Audience (``aud``) that access tokens must contain.

:Type: string
:Default: ``"ballet"``
:Environment: ``BALLET_CORE_OIDC_AUDIENCE``

.. _reference-config-storage:

``[storage]`` (core)
--------------------

``path``
~~~~~~~~

SQLite database file of Core. Its directory is created if missing;
migrations are applied at startup. Back it up together with
``[tokens] key_file``.

:Type: string (path)
:Default: ``"data/core.db"``
:Environment: ``BALLET_CORE_STORAGE_PATH``

.. _reference-config-rbac:

``[rbac]`` (core)
-----------------

``bootstrap_org_admins``
~~~~~~~~~~~~~~~~~~~~~~~~

Claim matchers granted ``org-admin`` at organization scope, independent
of stored role bindings (:doc:`/architecture/security`). Each entry is
``claim:value``; the claim must equal the value or, for list claims,
contain it. Without any entry, only stored bindings grant access — a
fresh installation is then unusable, so Core logs a warning.

:Type: list of strings
:Default: ``[]``
:Example: ``["groups:ballet-admins"]``
:Environment: ``BALLET_CORE_RBAC_BOOTSTRAP_ORG_ADMINS`` (comma-separated)

.. _reference-config-tokens:

``[tokens]`` (core)
-------------------

Run token signing (:doc:`run-tokens`).

``key_file``
~~~~~~~~~~~~

File holding Core's Ed25519 signing keys. Created with a fresh key (mode
``0600``) if it does not exist. Back it up: losing it invalidates every
issued run token.

:Type: string (path)
:Default: ``"data/token-keys.json"``
:Sensitive: yes — private keys
:Environment: ``BALLET_CORE_TOKENS_KEY_FILE``

.. _reference-config-secrets:

``[secrets]`` (core)
--------------------

``key_file``
~~~~~~~~~~~~

AES-256 key (base64) that encrypts secrets at rest, such as LLM
credentials. Generated with mode ``0600`` if missing. Back it up with
the database — without it, stored credentials cannot be decrypted.

:Type: string (path)
:Default: ``"data/secrets.key"``
:Sensitive: yes
:Environment: ``BALLET_CORE_SECRETS_KEY_FILE``

.. _reference-config-services:

``[services]`` (core)
---------------------

Identities of Ballet's own services (:ref:`run tokens <reference-run-tokens-services>`).

``tokens_dir``
~~~~~~~~~~~~~~

Directory where Core writes ``gateway.token``, ``knowledge.token`` and
``runner.token``.

:Type: string (path)
:Default: ``"data/service-tokens"``
:Sensitive: yes — the files are credentials
:Environment: ``BALLET_CORE_SERVICES_TOKENS_DIR``

``token_ttl``
~~~~~~~~~~~~~

Lifetime of service tokens; they are re-issued every quarter of it.

:Type: duration, at least ``1h``
:Default: ``"720h"`` (30 days)
:Environment: ``BALLET_CORE_SERVICES_TOKEN_TTL``

.. _reference-config-web:

``[web]`` (core)
----------------

``client_id``
~~~~~~~~~~~~~

Public OIDC client the web UI signs in with; published to the browser in
``/config.json`` together with ``[oidc] issuer_url``.

:Type: string
:Default: ``"ballet-web"``
:Environment: ``BALLET_CORE_WEB_CLIENT_ID``

``dir``
~~~~~~~

Directory of the built web UI (``make web-build`` → ``web/build``). When
set, Core serves it at ``/``, falling back to ``index.html`` for
client-side routes. Empty: the UI is not served (development uses the
Vite dev server).

:Type: string (path)
:Default: ``""``
:Environment: ``BALLET_CORE_WEB_DIR``

``[knowledge]`` (core)
----------------------

``url``
~~~~~~~

Base URL of the Knowledge service, to which Core forwards authorized
``/api/v1/customers/{customer}/knowledge/...`` requests.

:Type: string (URL)
:Default: ``"http://localhost:8081"``
:Environment: ``BALLET_CORE_KNOWLEDGE_URL``

``[gateway]`` (core)
--------------------

``url``
   Base URL of the LLM gateway. The planner sends its model calls there
   with a short-lived planner token (``kind: planner``,
   ``llm.invoke``), so the project's credential is used and usage is
   metered to the project.

   :Type: string (URL)
   :Default: ``"http://localhost:8082"``
   :Environment: ``BALLET_CORE_GATEWAY_URL``

``[planner]`` (core)
--------------------

The planner agent (:doc:`/architecture/decisions/0020-planner-runs-in-process-in-core`).
Read at start-up; changes need a restart.

``model``
   Anthropic model of every planner session. Default
   ``"claude-sonnet-5-5"``; ``BALLET_CORE_PLANNER_MODEL``.

``max_tokens``
   Maximum tokens of one model response. Default ``8192``;
   ``BALLET_CORE_PLANNER_MAX_TOKENS``.

``max_rounds``
   Tool rounds per turn: after this many rounds of tool calls the turn
   stops and the human is told to send a message to continue. Default
   ``20``; ``BALLET_CORE_PLANNER_MAX_ROUNDS``.

``skill``
   Name of the project skill whose resolved version is appended to the
   planner's built-in instructions (if the project has one). Default
   ``"planner"``; ``BALLET_CORE_PLANNER_SKILL``.

``compact_at_tokens``
   Estimated context size (tokens) above which the planner summarizes
   the conversation before the current human message, so long chats keep
   fitting the model's context window (:ref:`reference-planner-compaction`).
   ``0`` disables compaction. Default ``100000``;
   ``BALLET_CORE_PLANNER_COMPACT_AT_TOKENS``.

``[forge]`` (core)
------------------

``poll_interval``
   How often Core refreshes open pull requests from their forge (at
   least ``10s``). Default ``1m``; ``BALLET_CORE_FORGE_POLL_INTERVAL``.

.. _reference-config-scheduler:

``[scheduler]`` (core)
----------------------

The scheduler starts the pipelines of runnable tickets on its own (see
:ref:`reference-pipelines-scheduling`).

``enabled``
   Start runnable tickets automatically. Default ``true``;
   ``BALLET_CORE_SCHEDULER_ENABLED``. With ``false`` pipelines only start
   by hand.

``max_active``
   Flows occupying a slot at once, across all projects (at least 1).
   Default ``4``; ``BALLET_CORE_SCHEDULER_MAX_ACTIVE``.

``max_active_per_project``
   The same limit per project (at least 1). Default ``2``;
   ``BALLET_CORE_SCHEDULER_MAX_ACTIVE_PER_PROJECT``.

``interval``
   How often the scheduler looks for runnable tickets (at least ``1s``);
   it also looks whenever a flow moves on. Default ``10s``;
   ``BALLET_CORE_SCHEDULER_INTERVAL``.

``[agents]`` (core)
-------------------

Coding-agent runs (:ref:`reference-runners-agents`).

``gateway_url`` / ``knowledge_mcp_url``
   The LLM gateway and the knowledge MCP endpoint **as reached from inside
   run sessions** (containers may need other host names than Core).
   Default ``gateway.url`` and ``knowledge.url`` + ``/mcp``;
   ``BALLET_CORE_AGENTS_GATEWAY_URL``, ``…_KNOWLEDGE_MCP_URL``.

``tracker_mcp_url``
   Core's tracker MCP endpoint as reached from run sessions. Default
   ``"http://localhost:8080/mcp/tracker"``;
   ``BALLET_CORE_AGENTS_TRACKER_MCP_URL``.

``claude_command``
   The Claude Code executable in the session. Default ``"claude"``;
   ``BALLET_CORE_AGENTS_CLAUDE_COMMAND``.

``model``
   Model of agent sessions; ``""`` (default) lets the agent choose.
   ``BALLET_CORE_AGENTS_MODEL``.

``run_token_ttl``
   Validity of run tokens (at least ``10m``; cover the longest run).
   Default ``3h``; ``BALLET_CORE_AGENTS_RUN_TOKEN_TTL``.

``[core]`` and ``[storage]`` (knowledge)
----------------------------------------

``core.url``
   Base URL of Core; Knowledge verifies tokens against Core's JWKS.
   Default ``"http://localhost:8080"``; ``BALLET_KNOWLEDGE_CORE_URL``.

``storage.path``
   Knowledge's SQLite database. Default ``"data/knowledge.db"``;
   ``BALLET_KNOWLEDGE_STORAGE_PATH``.

``embeddings.mode`` / ``embeddings.model`` (knowledge)
   ``local`` (default): the ``hash-256`` embedder in-process. ``gateway``:
   embed through the LLM gateway (``embeddings.gateway_url``, default
   ``http://localhost:8082``) with the knowledge service token
   (``embeddings.token_file``, default
   ``data/service-tokens/knowledge.token``), attributed to each entry's
   customer; ``model`` names any model the gateway serves (default
   ``hash-256``). Changing the model re-embeds all entries.
   ``BALLET_KNOWLEDGE_EMBEDDINGS_MODE``, ``…_MODEL``, ``…_GATEWAY_URL``,
   ``…_TOKEN_FILE``.

``search.max_distance`` (knowledge)
   Semantic matches farther than this cosine distance are ignored
   (default ``0.85``, suited to ``hash-256``; real embedding models
   usually need a lower value). ``BALLET_KNOWLEDGE_SEARCH_MAX_DISTANCE``.

Knowledge accepts only Core-issued tokens with audience ``knowledge``
whose customer matches the requested space.

.. _reference-config-gateway:

``[core]`` (gateway)
--------------------

``url``
~~~~~~~

Base URL of Core: JWKS for run tokens and the internal API for
credentials.

:Type: string (URL)
:Default: ``"http://localhost:8080"``
:Environment: ``BALLET_GATEWAY_CORE_URL``

``token_file``
~~~~~~~~~~~~~~

The gateway's service token written by Core
(:ref:`reference-config-services`).

:Type: string (path)
:Default: ``"data/service-tokens/gateway.token"``
:Sensitive: yes
:Environment: ``BALLET_GATEWAY_CORE_TOKEN_FILE``

``[anthropic]`` (gateway)
-------------------------

``url``
~~~~~~~

Anthropic API base URL used when a credential has no ``base_url``.

:Type: string (URL)
:Default: ``"https://api.anthropic.com"``
:Environment: ``BALLET_GATEWAY_ANTHROPIC_URL``

``[openai]`` (gateway)
----------------------

``url``
~~~~~~~

OpenAI-compatible API base URL used for embeddings when a credential has
no ``base_url``.

:Type: string (URL)
:Default: ``"https://api.openai.com"``
:Environment: ``BALLET_GATEWAY_OPENAI_URL``

``[embeddings]`` (gateway)
--------------------------

``default_model``
~~~~~~~~~~~~~~~~~

Model used when an embeddings request names none. ``hash-256`` is
computed locally without a provider.

:Type: string
:Default: ``"hash-256"``
:Environment: ``BALLET_GATEWAY_EMBEDDINGS_DEFAULT_MODEL``

Operational endpoints
---------------------

Every service serves these on its ``[server] addr``:

.. list-table::
   :header-rows: 1

   * - Endpoint
     - Meaning
   * - ``GET /healthz``
     - Liveness: ``200 {"service":"<name>","status":"ok"}`` while the
       process runs.
   * - ``GET /readyz``
     - Readiness: ``200`` when every dependency check passes, ``503``
       otherwise, with per-check ``ok``/``failing``. Failure details are
       logged, not returned. Core checks ``database``.
   * - ``GET /metrics``
     - Prometheus metrics: Go runtime, process, and
       ``ballet_build_info{service,version}``; Core also
       ``ballet_planner_compactions_total``.
   * - ``GET /.well-known/jwks.json`` (core only)
     - Public keys for verifying run tokens (:doc:`run-tokens`).
   * - ``GET /config.json`` (core only)
     - Public runtime configuration of the web UI:
       ``{"oidc": {"issuer", "client_id"}}``.
