Workflow
========

End to end, work flows from a conversation to merged, documented code.

.. mermaid::

   flowchart TD
     H[Human engineer] -- chat --> P[Planner agent]
     P -- reads/writes docs --> KB[(Knowledge)]
     P -- proposes --> C[Plan changeset]
     C -- human approves --> T[Tickets + dependencies]
     T --> S{Scheduler: ready?}
     S -- yes --> PL[Ticket pipeline<br/>Implement → Review → Verify → Integrate<br/>each a separate session]
     PL -- question --> Q[Planner / human inbox]
     Q -- answer --> PL
     PL --> D[Done: merged + documented]

Planning: human + planner agent
-------------------------------

Planning is the part of the process where a human is expected to be
present. The human engineer talks to a **planner agent** — a
product/project manager for one project. The planner:

- researches and writes documentation in the knowledge base;
- turns intent into milestones, epics and tickets with dependencies and
  acceptance criteria;
- consults the lineage graph to understand how a feature evolved;
- proposes changes as a **plan changeset**.

The planner never changes the plan silently. A human approves a changeset
— in the same chat — as a whole or item by item; only approved items are
created. A changeset is a list of operations (create an item, update an
item, add a dependency) that may refer to items created earlier in the
same changeset; approving an operation requires approving the ones it
refers to, and the approved operations are applied atomically
(:ref:`reference-rest-changesets`). The planner may *suggest* an execution policy for a ticket but
cannot grant autonomy — only a human can.

See :doc:`/architecture/decisions/0020-planner-runs-in-process-in-core`.

Execution: autonomous pipeline
------------------------------

When a ticket becomes ready (:doc:`scheduling`), Ballet runs it through
the ticket pipeline (:doc:`pipeline`). Every stage session:

#. runs on an agent of the project's pool — built from its devcontainer —
   in a fresh workspace;
#. gets the repository checked out on the ticket's branch, named by the
   project's branch convention;
#. gets the skills for its stage and the MCP configuration
   (:doc:`skills`);
#. gets its onboarding bundle: ticket, epic, related tickets, relevant
   knowledge, feature lineage, and the reports of earlier stages;
#. does its work and ends with a structured report.

Agents report progress and discovered work through the tracker MCP.
Discovered work becomes a *proposal* for the planner and human — a
session never creates tickets directly. When an agent cannot proceed, it
asks a question (:doc:`questions`).

Review and merge policy
-----------------------

Code review happens **on the git platform**, not in Ballet. Ballet links
every ticket to its pull request, follows its state (review, CI, merged)
through a forge adapter, and acts on it according to the ticket's policy
(:doc:`/architecture/decisions/0007-review-on-git-platforms-via-forge-adapters`).

Each ticket carries an **execution policy**, defaulted from the
project's process profile; the project's pipeline implements it
(:doc:`pipeline`):

.. list-table::
   :header-rows: 1

   * - Setting
     - Values
     - Meaning
   * - Review mode
     - ``agent`` | ``agent+human``
     - The Review stage always runs. ``agent+human`` additionally waits
       for a human approval on the pull request.
   * - Merge mode
     - ``auto`` | ``manual``
     - ``auto``: the Integrate stage merges through the platform API when
       all gates pass. ``manual``: a human merges on the platform; Ballet
       detects it.

The default is ``agent`` + ``auto`` — fully autonomous. Requiring human
review or manual merge is a per-project or per-ticket choice
(:doc:`/architecture/decisions/0008-configurable-review-and-merge-policy`).

Done means documented
---------------------

A ticket reaches ``Done`` only when:

- the change is merged;
- every pipeline stage passed;
- the knowledge write-back exists: affected docs updated, decisions
  recorded, technical debt filed as ``tech-debt`` proposals.

Ticket states
-------------

.. code-block:: text

   backlog → ready → in_progress (stage: implement → review → … per pipeline) → done
                          │  ↑
                          ↓  │  answer
                  waiting_for_answer

   active states → paused → ready      any → cancelled      done/cancelled → backlog

Because pipelines are configured per project (:doc:`pipeline`), the
ticket state does not name stages; a ticket ``in_progress`` records its
current pipeline **stage** separately. Humans move tickets between
``backlog``, ``ready``, ``paused``, ``done`` and ``cancelled``; the
orchestrator sets ``in_progress`` and ``waiting_for_answer``. Whether a
ready ticket can actually start depends on its dependencies
(:doc:`scheduling`).
