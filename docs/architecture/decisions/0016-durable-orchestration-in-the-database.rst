ADR-0016: Durable Orchestration in the Database
===============================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Ballet runs ticket pipelines unattended for long periods: many tickets,
each with several stages, retries, questions that wait for hours,
budgets and timeouts. Services restart (deploys, crashes); no work may
be lost or duplicated, and no ticket may stay stuck silently.

The database is SQLite now and rqlite later
(:doc:`0019-sqlite-first-rqlite-later`). rqlite does not support
interactive transactions: a request may execute several statements
atomically, but cannot read, decide in application code, and then write
within one transaction.

Decision
--------

Pipeline orchestration is implemented in Core as **explicit state
machines persisted in the Core database** plus a **durable job table in
the same database**:

- Every state transition is a single atomic batch of statements: update
  the entity with **optimistic concurrency** (``UPDATE … WHERE id = ? AND
  version = ?``), insert the event record, insert the jobs it causes
  (start stage, poll forge, enforce timeout). A batch that matches no row
  lost a race and is retried from a fresh read.
- One **orchestrator** instance (leader) claims and executes jobs. Job
  claims are also optimistic updates. No ``SELECT … FOR UPDATE`` or
  interactive transactions are used, so the same code runs on SQLite and
  rqlite.
- A **reconciler** periodically compares desired state with reality
  (containers, pull requests, sessions, Runner heartbeats) and repairs
  drift after crashes.
- All transitions are recorded as events, feeding timelines, metrics,
  WebSocket stream replay and the digest.

Alternatives Considered
-----------------------

Temporal (or similar workflow engine)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Durable execution, timers and retries built in.

Disadvantages:

- A substantial additional system to operate; contradicts the goal of
  easy development; state split between Temporal and Core.

In-memory orchestration
~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest to write.

Disadvantages:

- Loses work on restart; unacceptable for unattended operation.

Decision Criteria
-----------------

Reliability across restarts, portability between SQLite and rqlite,
operational simplicity, observability.

Rationale
---------

Ballet's workflows are a small set of well-defined state machines.
Atomic batches with optimistic concurrency are portable to rqlite, and a
single orchestrator removes most contention. The reconciler covers what
the database cannot (external containers and forges).

Consequences
------------

Positive
~~~~~~~~

- Restart-safe; transitions auditable; no extra infrastructure.

Negative
~~~~~~~~

- Timers, retries and backoff are implemented and tested by Ballet.
- Repository code must avoid interactive transactions.

Risks
~~~~~

- A single orchestrator limits throughput; acceptable at expected scale
  (tens of concurrent sessions).

Follow-up
~~~~~~~~~

- Leader election when Core runs as several instances on rqlite.

Validation
----------

Failure tests: kill Core, Runner and containers at each pipeline step;
verify that every ticket resumes or is escalated, with no duplicate
merges.

References
----------

- :doc:`/concepts/unattended-operation`
- :doc:`0019-sqlite-first-rqlite-later`
