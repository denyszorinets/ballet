ADR-0012: PostgreSQL for Core and Knowledge
===========================================

:Status: Rejected
:Date: 2026-10-01

.. note::

   Rejected in favour of
   :doc:`0019-sqlite-first-rqlite-later` to keep development simple.

Context
-------

Core needs relational data with transactions (tracker, DAG, gates,
usage records). Knowledge needs full-text search, graph-like link
queries, and possibly vector search later. Both must be operable by a
small team; usage data must be queryable from Grafana.

Decision
--------

Both services use **PostgreSQL**, as separate databases (separate
instances in production). Knowledge uses PostgreSQL full-text search and
recursive queries for lineage; ``pgvector`` may be added for semantic
search.

Alternatives Considered
-----------------------

SQLite
~~~~~~

Advantages:

- Zero operations.

Disadvantages:

- Concurrency limits with many runs; weaker search; no Grafana data
  source in typical setups.

Dedicated graph or search engine for Knowledge
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Specialized query power.

Disadvantages:

- Another technology to operate before the need is proven.

Decision Criteria
-----------------

Functional fit, operational simplicity, ecosystem maturity.

Rationale
---------

PostgreSQL covers relational, full-text, recursive-graph and vector
needs with one well-known technology.

Consequences
------------

Positive
~~~~~~~~

- One database technology to run, back up and monitor.

Negative
~~~~~~~~

- Graph queries are less expressive than in a graph database.

Risks
~~~~~

- Lineage queries may become slow at scale; revisit with measurements.

Follow-up
~~~~~~~~~

- Choose migration tooling and query layer.

References
----------

- :doc:`0005-knowledge-as-separate-service-with-mcp`
