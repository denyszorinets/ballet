Knowledge
=========

The knowledge base is Ballet's long-term memory. It is a separate service
with its own database, used by agents through MCP and by humans through
the UI (:doc:`/architecture/decisions/0005-knowledge-as-separate-service-with-mcp`).

Scope: one space per customer
-----------------------------

Each customer has one **knowledge space** shared by all of that
customer's projects. Knowledge never crosses between customers: every
request is bound to a single customer by the caller's identity, and the
service refuses anything else.

Organization-wide engineering knowledge (standards, practices) is *not*
stored in customer spaces; it is distributed as :doc:`skills`.

What is stored
--------------

- **Documents** — product docs, specifications, guides.
- **Decisions** — architectural and product decisions with rationale.
- **Notes / memory** — facts agents and humans learned that do not fit a
  document.
- **Debt records** — known technical debt with context.
- **Links** to tracker entities (tickets, epics) by ID, and to pull
  requests and commits on the git platform by URL.

Every entry records which project(s) it concerns, who wrote it (human or
run), and when.

Lineage graph
-------------

Entries and tracker entities form a graph:

.. code-block:: text

   ticket ACME-42 ──implements──▶ doc "Invoice export"
   ticket ACME-77 ──evolves─────▶ doc "Invoice export"
   decision D-12  ──supersedes──▶ decision D-03
   ACME-77        ──depends-on──▶ ACME-42
   doc            ──mentions────▶ doc

When a new business ticket arrives, an agent asks for its **lineage**:
which earlier tickets, documents and decisions concern the same feature,
how it evolved, and what it connects to. The answer is a bounded subgraph
with summaries — the core of the onboarding bundle.

Search
------

Hybrid search: full-text and vector (semantic) search combined, over all
entries in the customer's space
(:doc:`/architecture/decisions/0021-hybrid-vector-search-over-all-content`).

- The full-text index is maintained by database triggers on every write.
- Embeddings are computed asynchronously by an indexer that re-embeds
  entries whose content or embedding model changed; a new entry is
  searchable by text immediately and semantically within seconds.
- Semantic candidates farther than a cosine distance threshold are
  dropped, so unrelated entries do not surface.
- If embedding a query fails, search degrades to full-text only.

Write-back
----------

Every run must leave knowledge behind; the ``knowledge write-back`` gate
checks that the run created or updated entries linked to its ticket.
