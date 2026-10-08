Ticket Pipeline
===============

Every ticket passes through a pipeline of **stages**. Each agent stage is
executed by a **separate agent session** in a fresh workspace, with its
own skills and role. A session never evaluates its own work.

The pipeline is **defined per project**. Ballet ships a default
four-stage template; each project adopts, changes or replaces it.

.. mermaid::

   flowchart LR
     I[Implement] --> R[Review]
     R -- approve --> V[Verify]
     R -- changes requested --> I
     V -- pass --> G[Integrate]
     V -- fail --> I
     G -- conflict / CI red --> I
     G -- merged --> D[Done]

*The default template.*

Why separate sessions
---------------------

An agent that wrote the code is biased toward believing it is correct: it
remembers its intentions, not what it actually did. A fresh session sees
only what is really there — the ticket, the code, the evidence — exactly
as a human reviewer or QA engineer would.

Stages may additionally use a **different runtime or model** than the
implementer to reduce shared blind spots.

Stages communicate through artifacts, not transcripts
-----------------------------------------------------

A stage receives:

- the ticket, acceptance criteria, epic and lineage (as every run does);
- the branch and pull request;
- the **structured reports** of previous stages (e.g. review comments,
  verification failures).

It does *not* receive previous sessions' conversations or reasoning.
Each stage ends with a structured report stored in Ballet and posted to
the pull request.

Fixed versus configurable
-------------------------

Ballet enforces a few invariants for every project; everything else is
the project's choice.

.. list-table::
   :header-rows: 1

   * - Fixed by Ballet
     - Configured per project
   * - Each agent stage is a separate session in a fresh workspace.
     - Which stages exist, their order and transitions.
   * - Stages exchange artifacts and reports, never transcripts.
     - Each stage's role instructions and skills.
   * - Questions, budgets, timeouts and iteration limits apply.
     - Runtime and model per stage; the project's agent pool.
   * - Every transition is recorded and observable.
     - Branch naming, base branch, merge strategy, gates.

Ballet assumes no development process of its own: branching model,
testing approach, documentation format and release practice come from
the project's skills and pipeline, not from Ballet.

Pipeline definition
-------------------

A pipeline is a versioned definition stored in Core, edited in the UI
and importable/exportable as YAML. Illustrative shape:

.. code-block:: yaml

   name: default
   version: 3
   branch: "feature/{ticket}_{slug}"     # project's own convention
   base: develop
   iteration_limit: 3
   stages:
     - id: implement
       kind: agent
       skills: [implementer, project-conventions]
       runtime: claude-code
       next: { done: review }
     - id: review
       kind: agent
       skills: [reviewer, project-conventions]
       runtime: codex                     # different eyes
       next: { approve: verify, changes_requested: implement }
     - id: verify
       kind: agent
       skills: [verifier]
       next: { pass: integrate, fail: implement }
     - id: integrate
       kind: agent
       skills: [integrator]
       next: { merged: done, returned: implement }

Stage kinds:

``agent``
   A fresh agent session with the given skills; ends with a report whose
   outcome selects the next stage.

``human``
   Waits for a human decision in Ballet (e.g. approve a risky change) —
   raised like a question.

``platform``
   Waits for an event on the git platform (human PR approval, CI result,
   merge) through the forge adapter.

The ticket's execution policy (:doc:`workflow`) can toggle optional
stages, e.g. adding a ``platform`` wait for human PR approval.

A project may map different pipelines to ticket types (``docs`` without
Verify; ``spike`` ending in a knowledge report). A ticket keeps the
pipeline version it started with.

Loops and limits
----------------

Every return to an earlier stage starts a new session with the reports
that caused it. The **iteration limit** bounds round trips; when it is
reached, Ballet raises a question instead of trying again
(:doc:`questions`).

See :doc:`/architecture/decisions/0014-separate-session-per-pipeline-stage`
and :doc:`/architecture/decisions/0017-pipelines-defined-per-project`.
