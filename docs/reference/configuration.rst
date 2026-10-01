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
       ``ballet_build_info{service,version}``.
   * - ``GET /.well-known/jwks.json`` (core only)
     - Public keys for verifying run tokens (:doc:`run-tokens`).
   * - ``GET /config.json`` (core only)
     - Public runtime configuration of the web UI:
       ``{"oidc": {"issuer", "client_id"}}``.
