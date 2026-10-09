Knowledge
=========

The knowledge base is Ballet's long-term memory. It is a separate service
with its own database, used by agents through MCP and by humans through
the UI (:doc:`/architecture/decisions/0005-knowledge-as-separate-service-with-mcp`).

Scope: one space per organization
---------------------------------

Each organization has one **knowledge space** shared by all of that
organization's projects. Knowledge never crosses between organizations: every
request is bound to a single organization by the caller's identity, and the
service refuses anything else.

Platform-wide engineering knowledge (standards, practices) is *not*
stored in organization spaces; it is distributed as :doc:`skills`.

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

Lineage
-------

How the product's features appeared, changed and branched is recorded in
the :doc:`feature-map`, next to the tickets that change them. Knowledge
entries link to tickets, and through them to features.

Search
------

Hybrid search: full-text and vector (semantic) search combined, over all
entries in the organization's space
(:doc:`/architecture/decisions/0021-hybrid-vector-search-over-all-content`).

- The full-text index is maintained by database triggers on every write.
- Embeddings are computed asynchronously by an indexer that re-embeds
  entries whose content or embedding model changed; a new entry is
  searchable by text immediately and semantically within seconds.
- Semantic candidates farther than a cosine distance threshold are
  dropped, so unrelated entries do not surface.
- If embedding a query fails, search degrades to full-text only.

In the UI
---------

The organization page links to the organization's knowledge space
(``/organizations/{organization}/knowledge``). Anyone with ``knowledge.read`` on
the organization can list, filter (kind, project) and search entries and read
an entry with its version history; ``knowledge.write`` adds creating and
editing entries in a Markdown editor with preview. Each tracker item page
lists the entries linked to it and offers *Add knowledge*, which creates
an entry pre-linked to the item. The UI reaches Knowledge through Core
(:doc:`/architecture/decisions/0022-humans-reach-knowledge-through-core`).

Write-back
----------

Every run must leave knowledge behind; the ``knowledge write-back`` gate
checks that the run created or updated entries linked to its ticket.
