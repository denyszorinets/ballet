ADR-0019: SQLite First, rqlite Later
====================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Core and Knowledge each need a database. Development should be easy: no
database server to install for local work or tests. Later, Ballet needs
high availability for long unattended operation. Knowledge requires
full-text and vector search
(:doc:`0021-hybrid-vector-search-over-all-content`).

Decision
--------

- Both services use **SQLite** — one database file each (Core and
  Knowledge stay separate, :doc:`0005-knowledge-as-separate-service-with-mcp`).
- SQL is written in the SQLite dialect, including FTS5 for full-text
  search and the ``sqlite-vec`` extension for vectors.
- **rqlite** (distributed, Raft-replicated SQLite) is the planned
  production/high-availability backend. Because it executes the same SQL
  dialect and supports SQLite extensions, the schema and queries carry
  over; only the driver changes.
- To stay rqlite-compatible from day one, persistence code uses **no
  interactive transactions**: writes are single atomic statement batches
  with optimistic concurrency
  (:doc:`0016-durable-orchestration-in-the-database`).
- Persistence sits behind repository interfaces in each service's
  infrastructure layer; the SQLite and rqlite implementations share SQL.
- PostgreSQL is not planned. If ever needed, it is a new ADR.

Alternatives Considered
-----------------------

PostgreSQL from the start
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Mature HA, ``pgvector``, rich concurrency features.

Disadvantages:

- A server to run for every developer, test and small installation.

SQLite only, forever
~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest.

Disadvantages:

- No high availability for a system meant to run unattended.

Decision Criteria
-----------------

Development simplicity, path to high availability, search support,
operational cost.

Rationale
---------

SQLite makes development and tests trivial; rqlite offers HA without
changing the SQL dialect. Designing writes around atomic batches now
avoids a rewrite later.

Consequences
------------

Positive
~~~~~~~~

- Zero-setup development; tests run against real databases in
  temporary files.

Negative
~~~~~~~~

- Single writer per database in SQLite; write-heavy paths (session
  output, usage records) need batching.
- No interactive transactions in application code.

Risks
~~~~~

- ``sqlite-vec`` and FTS5 availability in the chosen Go driver and in
  rqlite must be verified.

Follow-up
~~~~~~~~~

- Choose the Go SQLite driver (cgo vs. pure Go/WASM) based on
  ``sqlite-vec`` and FTS5 support.
- Choose a migration tool.

Validation
----------

Spike: FTS5 + ``sqlite-vec`` query through the chosen Go driver, and the
same schema and queries executed on rqlite.

References
----------

- :doc:`0012-postgresql-for-core-and-knowledge` (rejected)
- :doc:`0016-durable-orchestration-in-the-database`
