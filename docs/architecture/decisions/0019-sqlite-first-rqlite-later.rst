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
  search and the vector functions of ``sqlite-vec``
  (``vec_f32``, ``vec_distance_cosine``) for vectors.
- The Go driver is **modernc.org/sqlite** (pure Go, no cgo). Because it
  cannot load C extensions, Ballet registers Go implementations of the
  ``sqlite-vec`` functions it uses, with identical names and semantics
  (little-endian ``float32`` blobs).
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

- The Go vector functions must stay semantically identical to
  ``sqlite-vec``; cover them with tests against reference values.

Follow-up
~~~~~~~~~

- Choose a migration tool.
- When adding rqlite: load the ``sqlite-vec`` extension
  (``-extensions-path``) and keep the Go functions in sync with it.

Validation
----------

Spike :issue:`28` (code in ``spikes/28-sqlite-search``), 2026-10-01:

- **ncruces/go-sqlite3** (pure Go, WASM): the ``sqlite-vec`` Go bindings
  are pinned to an old ncruces release and fail at runtime (WASM import
  signature mismatch). Its newer ``vec1`` extension (sqlite.org/vec1) is
  not available in rqlite. Rejected.
- **mattn/go-sqlite3** (cgo) + ``sqlite-vec`` cgo bindings: builds only
  with system SQLite headers installed (``sqlite-devel`` /
  ``libsqlite3-dev``); adds cgo to every build. Rejected.
- **modernc.org/sqlite** (pure Go, ``CGO_ENABLED=0``): SQLite 3.53.4 with
  FTS5. Vector distance via a registered Go ``vec_distance_cosine``.
  Hybrid FTS5 + vector query with reciprocal rank fusion works;
  multi-statement atomic inserts work. **Chosen.**
- **rqlite 10.4.0** with ``sqlite-vec`` 0.1.9 loadable extension: the
  identical schema, atomic batch (``/db/execute?transaction``) and hybrid
  query run unchanged and return identical rankings and scores. A
  conditional ``UPDATE … WHERE version = ?`` reports affected rows, as
  required for optimistic concurrency.

References
----------

- :doc:`0012-postgresql-for-core-and-knowledge` (rejected)
- :doc:`0016-durable-orchestration-in-the-database`
