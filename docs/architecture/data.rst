Data
====

Databases
---------

Each stateful service owns one SQLite database
(:doc:`decisions/0019-sqlite-first-rqlite-later`); services never read
each other's databases.

.. list-table::
   :header-rows: 1

   * - Service
     - Database
     - Contents
   * - Core
     - ``[storage] path`` (default ``data/core.db``)
     - Tenancy, tracker, pipelines, runs, questions, skills, usage,
       events
   * - Knowledge
     - ``[storage] path`` (default ``data/knowledge.db``)
     - Entries per organization space with all versions (search index and
       embeddings follow)

Writes follow the batch and optimistic-concurrency rules in
:doc:`/development/persistence`.

Event log
---------

Every change to a tracked entity produces **exactly one event**, written
in the same atomic batch as the change — a change without its event (or
the reverse) cannot be committed.

.. list-table::
   :header-rows: 1

   * - Field
     - Meaning
   * - ``seq``
     - Monotonic sequence number, never reused; consumers resume from the
       last ``seq`` they processed (realtime streams, digest)
   * - ``id``
     - UUIDv7
   * - ``occurred_at``
     - UTC timestamp
   * - ``organization_id``, ``project_id``
     - Scope, for isolation and per-project streams
   * - ``entity_type``, ``entity_id``
     - The changed entity (``ticket``, ``project``, …)
   * - ``type``
     - ``<entity>.<change>``, e.g. ``ticket.created``,
       ``ticket.state_changed``
   * - ``actor_kind``, ``actor_sub``, ``acting_for``
     - Who caused the change: ``human`` (OIDC subject), ``service`` (run
       token subject; ``acting_for`` holds the human behind a planner) or
       ``system`` (Core itself)
   * - ``payload``
     - JSON with the change's details

Events are append-only. Because every write is serialized, ``seq``
order equals commit order: a reader that has seen ``seq`` N has seen
every event up to N. Core's event feed tails the log (every ~200 ms) and
fans new events out to realtime subscriptions, independent of which
component or Core instance wrote them.

They are the source for entity history, the realtime API's change streams
(:doc:`decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`),
timelines and the digest.

Durable jobs
------------

Work that must survive restarts — starting a pipeline stage, checking a
pull request later, enforcing a timeout — is a **job** in Core's database
(:doc:`decisions/0016-durable-orchestration-in-the-database`). A job has a
kind, a JSON payload, a time to run at, attempts and an optional
**dedupe key**: at most one pending or running job exists per key, so
"check X later" can be enqueued idempotently.

Jobs are inserted in the same atomic batch as the transition that causes
them. The **orchestrator** polls for due jobs (every second, and at once
when work is enqueued), **claims** each with an optimistic update
(``status = running``, a 5-minute lease) and runs the handler of its kind,
at most 8 at a time:

- success → ``done``;
- an error → ``pending`` again after a backoff (2, 4, 8 … seconds, at most
  10 minutes), until ``max_attempts`` is reached → ``dead``;
- a permanent error (or no handler for the kind) → ``dead`` at once;
- a crash or restart mid-job → the lease expires and the job is claimed
  again; handlers must therefore be idempotent.

Claims are optimistic, so several orchestrators never run the same claim
twice. Results are counted in ``ballet_jobs_total{kind, result}``
(``done``, ``retry``, ``dead``); dead jobs keep their last error.

What jobs cannot cover — a run that ended while Core was down before its
follow-up job was queued, a job that died, a run that hangs — the
**reconciler** repairs: once a minute it compares every active flow with
its ticket, its run and its jobs (see
:ref:`reference-pipelines-recovery`).

Identifiers
-----------

Entities use UUIDv7 identifiers (time-ordered). Human-facing keys, such
as project keys (``ACME``) and ticket keys (``ACME-42``), are separate
attributes.
