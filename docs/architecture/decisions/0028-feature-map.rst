ADR-0028: The Feature Map — Features as a Versioned Graph in Core
=================================================================

:Status: Accepted
:Date: 2026-10-09

Context
-------

Software development is nebulous: features are added, changed, split and
dropped on the fly. Tickets record units of work, and knowledge entries
record documents and decisions, but nothing records what the *product*
is — which features exist, how each one appeared, how it changed over
time, what was derived from it, and what its state is now. That gap makes
onboarding hard for humans and agents alike.

Requirements gathered from the humans who use Ballet:

- features are recorded at **feature level** (a capability such as
  "Invoice export"), not as individual requirement statements;
- **tickets derive from features**: planning starts from the feature and
  the change to it, and tickets carry it out;
- a feature can span **several projects** of an organization (services of
  one solution);
- agents may change features; humans review them, and the organization
  decides how much agents may do;
- the map starts empty and grows with new work;
- the tool is mainly for humans: a graph view of how features evolved,
  including the state at any past date.

The graph is small: hundreds to a few thousand features per organization,
never millions. :doc:`0004-tenancy-organization-customer-project` and
:doc:`0005-knowledge-as-separate-service-with-mcp` planned a "lineage
graph" in the Knowledge service without designing it.

Decision
--------

Ballet keeps a **feature map** per organization in **Core**, stored in
plain SQLite tables (:doc:`0019-sqlite-first-rqlite-later`) and queried
with SQL — no graph database and no graph query language.

Model
~~~~~

**Feature**
   Organization-scoped, keyed ``F-<n>`` within the organization. Title,
   Markdown description (what the feature does *now*), status, and the
   projects that implement it.

**Revision**
   Every change to a feature — content, status or projects — appends an
   immutable revision: the full state after the change, the author (human,
   agent run or planner), the reason, and the cause (a changeset, ticket
   or run). The state of a feature at any time is its latest revision at
   that time.

**Status**
   ``planned`` → ``in_progress`` → ``live`` → ``changing`` → ``live`` …,
   and ``deprecated`` or ``removed`` at the end. Ballet moves a feature
   between the first four as its linked tickets start and finish; humans
   and agents set the others.

**Links between features**
   Typed and directed, each with the time it was added and, once removed,
   the time it ended: ``derived_from``, ``split_from``, ``merged_into``,
   ``supersedes``, ``depends_on``, ``relates``. Links valid at a time give
   the map at that time; ``derived_from`` and ``split_from`` give a
   feature's descendants.

**Tickets**
   A ticket lists the features it changes. Tickets come from changesets
   that create or update the features first, applied in the same
   transaction.

Governance
~~~~~~~~~~

Humans change features directly. Agents change them according to the
**feature policy** of the organization, which a project may override:

- ``direct`` (default): the change applies at once and its revision waits
  in a **review queue**, where a human confirms it or reverts it (a revert
  appends a revision restoring the previous content);
- ``proposal``: the change becomes a changeset a human approves;
- ``read_only``: agents cannot change features.

The planner always proposes feature changes in changesets.

Where it lives
~~~~~~~~~~~~~~

Core owns features because they are bound to tickets: changesets create
features and tickets atomically, status follows ticket progress, and
authorization, events and realtime updates already live there. The
Knowledge service keeps documents, decisions, notes and debt; features
link to them by ID.

Alternatives Considered
-----------------------

A graph database (Apache AGE, GraphQLite, Kuzu and its forks)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Cypher queries and built-in graph algorithms.

Disadvantages:

- None models time: revisions and validity would be modelled by hand
  either way, which is the hard part.
- AGE needs PostgreSQL; GraphQLite needs a C SQLite driver instead of the
  pure-Go one and does not fit rqlite; Kuzu was archived in October 2025
  and its forks are young.
- The graph is small: recursive SQL answers descendants and paths in
  milliseconds.

Features in the Knowledge service
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Knowledge already holds organization-scoped content and was to hold
  lineage.

Disadvantages:

- Changesets could not create features and tickets atomically, and status
  could not follow tickets without a second copy of their state.

Requirement-level nodes
~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Finer history and a more precise "current state".

Disadvantages:

- Much more discipline from agents and humans; the map becomes too dense
  to read. Acceptance criteria on tickets keep the detail.

Decision Criteria
-----------------

Fit with tickets and changesets, history at any time, simplicity, no new
runtime dependency, licensing.

Rationale
---------

The value is in the history and in the link between features and the work
that changes them. Both are relational, small, and belong next to the
tracker. Plain tables keep the dependency footprint and the operational
model unchanged.

Consequences
------------

Positive
~~~~~~~~

- One source of product truth that tickets, the planner, agents and the
  UI share; the map at any past date.
- No new database, no new language.

Negative
~~~~~~~~

- Graph algorithms beyond traversal (centrality, communities) must be
  written by hand if they are ever needed.

Risks
~~~~~

- Agents may produce noisy revisions. Mitigation: the review queue, the
  ``proposal`` and ``read_only`` policies.

Follow-up
~~~~~~~~~

- Revisit a graph engine if an organization's map grows beyond ~50 000
  features or traversals exceed 100 ms.

References
----------

- :doc:`/concepts/feature-map`
- :doc:`0027-tenancy-platform-organization-project`
- :issue:`197`
