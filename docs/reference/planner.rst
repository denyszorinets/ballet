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
and customer, and — if the project uses one — the SKILL.md of its
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
     - Propose create / update / dependency operations for the human to
       approve
     - ``tracker.write``
   * - ``list_skills``
     - The project's effective skills (name, scope, version)
     - ``skill.read``
   * - ``read_skill``
     - SKILL.md of a skill in the version the project uses
     - ``skill.read``
   * - ``search_knowledge``
     - Search the customer's knowledge base; optional ``kind``, ``limit``
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

Limits
------

A turn ends when the model stops calling tools, after
``planner.max_rounds`` rounds of tool calls (the human sends a message
to continue), or when the human cancels it. Model calls go through the
LLM gateway with the project's credential and are metered to the
project (:doc:`llm-gateway`).
