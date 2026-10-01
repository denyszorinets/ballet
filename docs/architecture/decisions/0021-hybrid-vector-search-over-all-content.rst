ADR-0021: Hybrid Vector Search over All Content
===============================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Agents and humans must find relevant context quickly: knowledge entries,
skills, and past tickets and their reports. Keyword search misses
conceptually related content ("invoice export" vs. "billing CSV").
Onboarding bundles and the planner depend on good retrieval.

Decision
--------

All searchable content is indexed for **hybrid search** — full-text
(SQLite FTS5) plus vector similarity, with results fused by reciprocal
rank:

- Embeddings are stored as ``float32`` blobs next to the content and
  compared with ``vec_distance_cosine`` (exact, brute-force scan; no
  approximate index). On embedded SQLite this function is a Go
  implementation; on rqlite it comes from the ``sqlite-vec`` extension
  (:doc:`0019-sqlite-first-rqlite-later`).

Indexed content:

- **Knowledge service:** knowledge entries, per customer space.
- **Core:** skills (respecting scope), tickets, epics, stage reports and
  answered questions (per customer).

Embeddings are computed asynchronously when content changes, through the
LLM gateway with a configured embeddings provider (metered and,
for customer data, using the customer's credentials). Each index records
the embedding model; changing the model triggers re-embedding. Search
respects the same customer scope as every other read.

Alternatives Considered
-----------------------

Full-text only
~~~~~~~~~~~~~~

Advantages:

- No embedding provider or cost.

Disadvantages:

- Weak recall for conceptual queries; worse onboarding bundles.

Dedicated vector database
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Scale and features.

Disadvantages:

- Another system to run; contradicts easy development.

Decision Criteria
-----------------

Retrieval quality, simplicity, isolation, cost.

Rationale
---------

Hybrid search combines exact matches with semantic recall, and both
indexes live in the same SQLite file as the content, so isolation and
backups stay simple.

Consequences
------------

Positive
~~~~~~~~

- Good retrieval from the first version for planner, agents and UI.

Negative
~~~~~~~~

- Requires an embeddings provider; customer content is sent to it.

Risks
~~~~~

- Brute-force vector search slows down at large volumes; acceptable at
  expected scale, measure.

Follow-up
~~~~~~~~~

- Choose the default embeddings provider (an OpenAI-compatible
  embeddings API covers hosted and local options).

Validation
----------

Spike :issue:`28`: hybrid FTS5 + cosine query with reciprocal rank fusion
returns identical results on embedded SQLite (modernc.org/sqlite with Go
vector functions) and on rqlite with ``sqlite-vec``. See
:doc:`0019-sqlite-first-rqlite-later` for details.

References
----------

- :doc:`/concepts/knowledge`
- :doc:`0019-sqlite-first-rqlite-later`
