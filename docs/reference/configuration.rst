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
