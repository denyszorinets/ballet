Planner
=======

The planner is a conversation loop inside Core
(:doc:`/architecture/decisions/0020-planner-runs-in-process-in-core`).
A human chats with it through the realtime API
(:ref:`reference-realtime-planner`); it answers with the model configured
in ``[planner]`` (:doc:`configuration`), calling the tools below.

Instructions
------------

The system prompt is Ballet's built-in planner instructions, the project
and organization, and — if the project uses one — the SKILL.md of its
``planner`` skill (``planner.skill``) in the version the project resolves
(pins apply, :doc:`/concepts/skills`). Write a ``planner`` skill to give a
project's planner its own process rules.

Authority
---------

Tools run as the **human** who sent the message: the planner can do
exactly what that human may do in the project, and nothing more. What it
does is recorded as the planner (``planner:<session>``) acting for that
human. It changes the plan **only** by proposing a changeset
(:ref:`reference-rest-changesets`); applying or rejecting one needs the
human, and the planner cannot set execution policies. Knowledge it writes
is attributed to the human.

A failing tool call (bad input, missing permission, unknown item) is
returned to the model as an error result, and the conversation continues.

Tools
-----

.. list-table::
   :header-rows: 1

   * - Tool
     - Does
     - Needs
   * - ``list_items``
     - The project's milestones, epics and tickets; optional ``kind``,
       ``state``, ``epic``, ``milestone`` filters
     - ``tracker.read``
   * - ``get_item``
     - One item with description, acceptance criteria, policy and
       dependencies
     - ``tracker.read``
   * - ``list_runnable``
     - Ready tickets whose blockers are resolved
     - ``tracker.read``
   * - ``search_project``
     - Hybrid search over the project's items and skills
     - ``tracker.read`` / ``skill.read``
   * - ``list_changesets``
     - The project's changesets, optionally by ``status``
     - ``tracker.read``
   * - ``propose_changeset``
     - Propose create / update / dependency operations, and feature
       operations (``create_feature``, ``update_feature``,
       ``link_features``, ``features`` on tickets), for the human to
       approve
     - ``tracker.write``
   * - ``list_features``
     - The organization's features (this project's by default;
       ``all_projects``, ``status``)
     - ``tracker.read``
   * - ``get_feature``
     - A feature's description, links, tickets and its latest ten
       revisions with reasons
     - ``tracker.read``
   * - ``list_skills``
     - The project's effective skills (name, scope, version)
     - ``skill.read``
   * - ``read_skill``
     - SKILL.md of a skill in the version the project uses
     - ``skill.read``
   * - ``search_knowledge``
     - Search the organization's knowledge base; optional ``kind``, ``limit``
     - ``knowledge.read``
   * - ``get_knowledge``
     - Read an entry
     - ``knowledge.read``
   * - ``create_knowledge``
     - Write a document, decision, note or debt record linked to the
       project and to items
     - ``knowledge.write``
   * - ``update_knowledge``
     - Change an entry (send the ``version`` read)
     - ``knowledge.write``

Each tool's input is a JSON object described to the model by a JSON
Schema; unknown fields are rejected. Outputs are JSON.

.. _reference-planner-compaction:

Context compaction
------------------

Before each model call the planner estimates the context the model would
see: the token usage the provider reported for the last answer plus the
messages added since (about four characters per token when no usage is
known). Above ``planner.compact_at_tokens`` it asks the model to
summarize everything **before the latest human message** (including any
earlier summary), stores the summary on the session, and from then on
sends the model that summary plus the newer messages. The current turn is
never summarized, so tool calls and their results stay together.

- The transcript stays complete: the UI and
  ``GET /api/v1/planner/sessions/{session}`` show every message.
- Each compaction records a ``planner.compacted`` event (``up_to``,
  ``estimated_tokens``), sends a ``compacted`` planner output, and
  increments the ``ballet_planner_compactions_total`` metric.
- A failed compaction is logged and the call proceeds uncompacted.
- A single turn whose own tool rounds outgrow the context cannot be
  compacted; ``planner.max_rounds`` bounds it.

Limits
------

A turn ends when the model stops calling tools, after
``planner.max_rounds`` rounds of tool calls (the human sends a message
to continue), or when the human cancels it. Model calls go through the
LLM gateway with the project's credential and are metered to the
project (:doc:`llm-gateway`).
