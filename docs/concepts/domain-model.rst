Domain Model
============

Tenancy
-------

.. code-block:: text

   Organization            the shop operating Ballet
   └── Customer            hard isolation boundary; owns a knowledge space
       └── Project         one product; one or more git repositories
           ├── Milestone
           ├── Epic
           └── Ticket
               └── Run

**Organization**
   The single operator of a Ballet installation. Owns organization-wide
   resources: the skill registry, devcontainer templates, agent runtime
   definitions and role bindings.

**Customer**
   Identified by an immutable key such as ``acme``. The isolation
   boundary. All of a customer's projects share one
   knowledge space; nothing — knowledge, tickets, runs, credentials —
   crosses between customers. Holds the default LLM credentials, which
   projects may override.

**Project**
   A product being built. Has a short, immutable, globally unique key
   (``ACME``) used in ticket keys (``ACME-123``), one or more git repositories, and a **process profile**
   (see below).

Planning entities
-----------------

**Milestone**
   A delivery target and synchronization point. On the Gantt chart a
   milestone is a join node: it completes when everything it depends on
   completes.

**Epic**
   A body of work with an objective, scope and high-level acceptance
   criteria. Groups tickets.

All three planning entities share one per-project key sequence
(``ACME-1``, ``ACME-2``, …).

**Ticket**
   The unit of agent work: small enough for one run, independently
   understandable, testable. Types: ``feature``, ``bug``, ``tech-debt``,
   ``docs``, ``spike``. Carries acceptance criteria and its
   **execution policy** (agent runtime, review mode, merge mode — see
   :doc:`workflow`).

**Dependency**
   A typed, directed edge between *any* two planning entities, across
   levels (ticket → milestone, epic → epic, ticket → ticket).

   - ``blocks`` — the target cannot start until the source is done;
     drives scheduling.
   - ``relates`` — informational; feeds the knowledge lineage graph.

   ``blocks`` edges must form a directed acyclic graph; Ballet rejects
   edges that would create a cycle.

Execution entities
------------------

**Run**
   One agent session executing one pipeline stage (Implement, Review,
   Verify, Integrate) of one ticket in one devcontainer. Records the
   stage, runtime, model, logs, token usage, cost, outcome and its
   structured **stage report**. A ticket has many runs: one per stage,
   plus rework and retries.

**Question**
   Raised by a run or the planner when it cannot proceed. Routed to the
   planner first, then to the human inbox. Has a sub-chat transcript and
   a final answer, which is also written to the knowledge base.

**Assumption**
   A decision an agent made on its own for a reversible, low-impact
   uncertainty. Listed on the ticket for later confirmation.

**Budget**
   A token or cost limit for a ticket, project (per day) or customer (per
   month).

**Pull Request**
   Ballet's record of the pull/merge request a run opened on the git
   platform: platform, URL, branch, review state, CI state, merge state.
   The review itself happens on the platform; Ballet links to it and
   tracks its state.

**Gate**
   A checkpoint a ticket must pass to move forward: tests green, review
   approved, knowledge write-back present, human approval. Gates are
   evaluated by Ballet, not self-reported by the agent.

**Plan Changeset**
   A batch of proposed planning changes (new or edited milestones, epics,
   tickets, dependencies) authored by the planner agent and approved —
   wholly or partly — by a human.

Configuration entities
----------------------

**Process Profile**
   Per project: which skills (and versions), which devcontainer template,
   which gates, and the default execution policy for new tickets.

**Skill**
   A versioned bundle of agent instructions, scoped to organization,
   customer or project (:doc:`skills`).

**Agent Runtime**
   A supported coding agent (Claude Code, Codex, opencode) with its
   adapter configuration.

Knowledge entities
------------------

Knowledge lives in the separate Knowledge service and references tracker
entities by ID (:doc:`knowledge`).
