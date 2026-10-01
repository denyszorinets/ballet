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
     - (planned) own file
     - Knowledge spaces and entries, embeddings

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
   * - ``customer_id``, ``project_id``
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

Events are append-only. They are the source for entity history, the
realtime API's change streams
(:doc:`decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`),
timelines and the digest.

Identifiers
-----------

Entities use UUIDv7 identifiers (time-ordered). Human-facing keys, such
as project keys (``ACME``) and ticket keys (``ACME-42``), are separate
attributes.
