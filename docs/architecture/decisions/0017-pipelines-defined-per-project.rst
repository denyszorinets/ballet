ADR-0017: Pipelines Defined per Project
=======================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Ballet serves many customers and projects with different development
processes: branching models, testing practices, release flows, and
review requirements. A process hard-coded into Ballet would fit some
projects and be wrong for others. The ticket pipeline
(:doc:`0014-separate-session-per-pipeline-stage`) must therefore be
adaptable from the first version, not retrofitted later.

Decision
--------

The ticket pipeline is **data, defined per project**:

- A pipeline definition lists stages (kind ``agent``, ``human`` or
  ``platform``), their skills, runtime, model and container template,
  and transitions keyed by stage outcome.
- Project-level settings — branch naming template, base branch, merge
  strategy, iteration limit — are part of the definition, not Ballet
  constants.
- Definitions are versioned; a ticket runs on the version it started
  with. Projects may map ticket types to different pipelines.
- Definitions are edited in the UI and importable/exportable as YAML.
- Ballet ships the four-stage Implement → Review → Verify → Integrate
  pipeline and neutral starter skills as a **default template**.

Ballet hard-codes only the invariants: separate session per agent stage,
artifact-only handoff, questions, budgets, timeouts, iteration limits,
and observability of every transition.

Alternatives Considered
-----------------------

Fixed four-stage pipeline with toggles
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simpler to implement and explain.

Disadvantages:

- Cannot express projects with different stages (security review,
  documentation review, staged release); expensive to change later.

Pipelines as code (scripts in the repository)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Unlimited flexibility; versioned with the code.

Disadvantages:

- Agents could modify the process that governs them; arbitrary code
  is hard to visualize, validate and observe.

Decision Criteria
-----------------

Fit across different projects, tamper resistance, observability,
implementation effort.

Rationale
---------

A small declarative model (stages, kinds, transitions) covers the
expected variation, can be validated and visualized, and stays outside
the reach of the agents it governs. Building it from the start avoids
baking one process into the orchestrator.

Consequences
------------

Positive
~~~~~~~~

- Each project runs its own process; Ballet carries no process
  assumptions.

Negative
~~~~~~~~

- The orchestrator must interpret definitions generically; validation
  (reachability, terminal states, cycles bounded by iteration limits) is
  required.

Risks
~~~~~

- Definitions may grow toward a general workflow language; keep the
  model minimal and extend only on demonstrated need.

Follow-up
~~~~~~~~~

- Specify the definition schema and validation rules.
- Write the default template and starter stage skills.

References
----------

- :doc:`/concepts/pipeline`
- :doc:`0010-central-skill-registry`
- :doc:`0016-durable-orchestration-in-the-database`
