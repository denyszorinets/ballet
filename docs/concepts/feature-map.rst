Feature map
===========

Software development is nebulous. Features are added, changed, split and
dropped as work goes on, and tickets alone do not say what the product
*is*. The **feature map** does: it records an organization's features as
a graph that keeps its whole history, so people — and agents — can see
how each feature appeared, how it changed, what grew out of it, and where
it stands now
(:doc:`/architecture/decisions/0028-feature-map`).

Features
--------

A **feature** is a capability of the product, such as "Invoice export".
It belongs to the organization, not to a single project: a feature may be
implemented by several projects — the services of one solution — and
lists them.

Each feature has a key (``F-12``, unique within the organization), a
title, a Markdown **description** of what it does *now*, the projects
that implement it, and a **status**:

.. list-table::
   :header-rows: 1

   * - Status
     - Meaning
   * - ``planned``
     - Agreed, no work started.
   * - ``in_progress``
     - Its first tickets are being worked on.
   * - ``live``
     - Delivered.
   * - ``changing``
     - Delivered, and tickets are changing it.
   * - ``deprecated``
     - Still there, to be removed.
   * - ``removed``
     - Gone.

Revisions: the history
----------------------

A feature is never edited in place. Every change — description, title,
status or projects — appends an immutable **revision** holding the full
state after the change, who made it (a person, an agent run or the
planner), why (the *reason*), and what caused it (a changeset, a ticket,
a run). The feature's history is its list of revisions; any two can be
compared.

Links: how features relate
--------------------------

Links between features are typed and directed, read "*from* type *to*":

``derived_from``
   The feature grew out of another one.
``split_from``
   The feature was split off another one.
``merged_into``
   The feature was merged into another one.
``supersedes``
   The feature replaces another one.
``depends_on``
   The feature needs another one.
``relates``
   Related, without a stronger meaning.

``derived_from`` and ``split_from`` form a feature's **lineage**: what
came from it, and what it came from. A link records when it was added
and, when removed, when it ended — so the map can be shown **as it was at
any date**: every feature in its latest revision at that date, and the
links valid then.

Tickets derive from features
----------------------------

.. note::

   Agents' own feature tools arrive with :issue:`194`; until then they
   read and change features through the planner's changesets.

Planning starts from features: the planner proposes the change to a
feature and the tickets that carry it out, together in one changeset.
Tickets list the features they change; as they start and finish, the
feature moves from ``planned`` to ``in_progress`` to ``live``, and a live
feature becomes ``changing`` while tickets change it. The map starts
empty and grows with new work.

.. _concepts-feature-map-policy:

Who may change features
-----------------------

People change features directly. Agents change them according to the
**feature policy** of their organization (organization page → *Feature
changes by agents*), which a project may override in its settings:

``direct`` (default)
   The change applies at once. Its revision waits in the **review
   queue**, where a person **confirms** it or **reverts** it — a revert
   appends a revision restoring the state before (and a reverted creation
   marks the feature removed). Only the latest revision can be reverted;
   otherwise edit the feature.
``proposal``
   The change becomes a changeset a person approves, like the planner's.
``read_only``
   Agents cannot change features.

Storage
-------

The map lives in Core next to the tracker, in plain database tables. It
is small — features, not lines of code — so ordinary queries answer
history and lineage; no graph database is needed.

See the REST API in :ref:`reference-rest-features`.
