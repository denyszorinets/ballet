ADR-0010: Central Skill Registry
================================

:Status: Accepted
:Date: 2026-10-01

.. note::

   Since :doc:`0027-tenancy-platform-organization-project`, customers are
   called **organizations** and the installation-wide level formerly
   called the organization is the **platform**; this record keeps the
   terms of its time.

Context
-------

Process is encoded as skills. The shop wants to share skills across
projects and customers, let customers or projects specialize them, and
avoid copying ``.claude/skills`` directories between repositories, where
they drift. Runs must use a predictable, pinned set of skills.

Decision
--------

Core hosts a **skill registry**:

- A skill is a named bundle (``SKILL.md`` plus files), published as
  immutable versions.
- Scopes: organization, customer, project. A lower scope may add skills
  or shadow a higher-scope skill with the same name.
- A project's process profile lists skills with pinned versions (or
  "latest" for the brave).
- At run start, the resolved set is written into the container by the
  agent adapter in the runtime's native layout.

Skills are stored **only in the Core database** — there is no git
repository, import or sync for skills. They are authored and versioned
in the Ballet UI (and via the REST API). Skills are indexed for hybrid
full-text and vector search
(:doc:`0021-hybrid-vector-search-over-all-content`) so the planner and
humans can find relevant skills.

The registry is a module of Core, not a separate service, until there is
a reason to split it.

Alternatives Considered
-----------------------

Skills in each repository
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No new component; versioned with code.

Disadvantages:

- Copies drift; no sharing; agents could edit their own process.

Skills in a dedicated git repository
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Git history and review for skills.

Disadvantages:

- Scoping and per-customer privacy are awkward.

Decision Criteria
-----------------

Sharing, privacy per customer, reproducibility, tamper resistance.

Rationale
---------

Central, versioned, scoped skills make process a managed asset; agents
cannot alter the process they are measured against.

Consequences
------------

Positive
~~~~~~~~

- Process changes roll out deliberately by bumping versions.

Negative
~~~~~~~~

- Skill authoring and version history must be built into the UI; there
  is no git history to fall back on.

Risks
~~~~~

- Runtimes other than Claude Code may not support skills natively;
  adapters may need to inline them into the prompt or rules files.

Follow-up
~~~~~~~~~

- Provide neutral starter skills for the default pipeline's stage roles,
  seeded into the database.
- The skills in Ballet's own repository describe how *Ballet* is
  developed; they become project-scoped skills of the Ballet project
  when Ballet manages itself, not organization defaults.
- Consider import/export from git for authoring.

References
----------

- :doc:`/concepts/skills`
