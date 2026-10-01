Runners
=======

A **Runner** executes agent sessions — **runs** — for Core
(:doc:`/architecture/decisions/0009-devcontainer-per-run-on-docker-or-podman`).
Runners connect to Core; Core never connects to them, so Runners can sit
behind NAT on any machine with a container runtime.

Runs
----

A run is one session executing one pipeline **stage** of one ticket. Its
**spec** says what to execute: ``command``, ``env``, ``workdir``,
``timeout_seconds`` and, for container backends, ``image``.

.. mermaid::

   stateDiagram-v2
     [*] --> queued
     queued --> starting: assigned to a Runner
     queued --> cancelled
     starting --> queued: Runner refused it
     starting --> running: session started
     starting --> failed
     running --> succeeded: exit code 0
     running --> failed: non-zero exit, error, timeout, Runner gone
     running --> cancelled
     succeeded --> [*]
     failed --> [*]
     cancelled --> [*]

- Core assigns queued runs, oldest first, to the connected Runner with
  the most free capacity.
- A run fails when its session exits non-zero, cannot start, times out
  (the spec's ``timeout_seconds`` or the Runner's
  ``runner.default_timeout``), or when its Runner stays disconnected for
  more than **2 minutes**.
- Output is kept per run up to **8 MiB**; later output is dropped.
- Every change records an event (``run.queued``, ``run.started``,
  ``run.finished`` with ``status`` and ``exit_code``).

Runs are listed on their ticket and over REST
(:ref:`reference-rest-runs`). Until the orchestrator queues runs
automatically (M5), people with ``run.manage`` queue them by hand.

Protocol
--------

JSON-RPC over WebSocket (the transport of :doc:`realtime-api`, same
authentication, refresh and heartbeats) at ``/runner/rpc``. The token is
Core's runner token (``data/service-tokens/runner.token``, audience
``core``, capability ``runner.connect``). Types are in
:repo:`kit/runnerproto/proto.go`.

.. list-table::
   :header-rows: 1

   * - Method
     - Direction
     - Params
   * - ``runner.hello``
     - Runner → Core
     - ``{"runner", "labels", "capacity", "active"}`` — first, once per
       connection. ``runner`` is unique among connected Runners
       (``-32009`` otherwise). Runs Core assigned to this Runner that are
       not in ``active`` fail.
   * - ``run.start``
     - Core → Runner
     - ``{"run", "spec"}``; an error result (e.g. at capacity) requeues
       the run
   * - ``run.cancel``
     - Core → Runner
     - ``{"run"}``
   * - ``run.status``
     - Runner → Core
     - ``{"run", "status": "running"}``
   * - ``run.log``
     - Runner → Core (notification)
     - ``{"run", "stream": "stdout"|"stderr"|"system", "text"}``
   * - ``run.finished``
     - Runner → Core
     - ``{"run", "exit_code", "error"?, "cancelled"?}``

A Runner reconnects with exponential backoff (0.5 s doubling to 30 s,
with jitter), introduces itself with the runs it still executes, and
delivers results it could not deliver before. Output produced while
disconnected is lost.

Configuration
-------------

``BALLET_RUNNER_*`` environment variables override the TOML file
(``-config``), as for every service (:doc:`configuration`).

.. list-table::
   :header-rows: 1

   * - Key
     - Default
     - Meaning
   * - ``core.url``
     - ``http://localhost:8080``
     - Core's base URL (``http`` → ``ws``)
   * - ``core.token_file``
     - ``data/service-tokens/runner.token``
     - The runner token Core writes; read on every connection
   * - ``runner.name``
     - host name
     - Unique name of this Runner
   * - ``runner.capacity``
     - ``2``
     - Concurrent runs
   * - ``runner.labels``
     - ``[]``
     - ``key=value`` labels; ``backend=<runner.backend>`` is added
   * - ``runner.backend``
     - ``docker``
     - Execution backend
   * - ``runner.default_timeout``
     - ``2h``
     - Timeout of runs whose spec sets none (at least ``1m``)
