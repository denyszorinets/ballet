ADR-0020: Planner Runs In-Process in Core
=========================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Humans plan with a planner agent in chat; the planner also answers
agent questions and drives sub-chats
(:doc:`0015-question-escalation-through-the-planner`). Its tools are
Ballet's own: plan changesets, tickets, knowledge, skills. It does not
need to build or run code. Simplicity is preferred over reusing the
worker execution model.

Decision
--------

The planner is a **conversation loop running inside the Core process**,
one per chat or sub-chat session:

- It calls the LLM through the LLM gateway (metered like any run) using
  the provider's API with tool use.
- Its tools are direct in-process calls to Core's application use cases
  and the Knowledge client (search, read, write knowledge; read plan and
  tickets; propose plan changesets; answer questions), authorized as the
  planner identity acting for the human.
- Transcripts are persisted; a session survives a Core restart by
  reloading its transcript.
- The model and the planner's instructions (skills) are configurable per
  project.

This is a deliberate, narrow exception to
:doc:`0003-orchestrate-existing-coding-agents`: workers remain external
coding agents; only the planner — which has no coding duties — is a
Ballet-owned loop.

Alternatives Considered
-----------------------

Planner as a coding agent in a container
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Same execution model as workers; access to the code.

Disadvantages:

- Long-lived containers and session bridging for a chat; more moving
  parts. See :doc:`0013-planner-runs-as-a-containerized-agent` (rejected).

Decision Criteria
-----------------

Simplicity, chat responsiveness, tool integration.

Rationale
---------

In-process tools need no MCP plumbing, the chat streams directly from
Core over WebSocket, and nothing has to be provisioned for a
conversation.

Consequences
------------

Positive
~~~~~~~~

- Fast, simple chat; full control over planner tools and streaming.

Negative
~~~~~~~~

- Ballet maintains one agent loop and its tool definitions.
- The planner cannot read the repository directly; it relies on the
  knowledge base and ticket reports.

Risks
~~~~~

- Long conversations hit context limits; compaction or summary into the
  knowledge base is needed.
- Planner load runs in Core; keep loops cancellable and bounded.

Follow-up
~~~~~~~~~

- Read-only repository access for the planner if knowledge proves
  insufficient.

References
----------

- :doc:`/concepts/workflow`
- :doc:`0011-llm-gateway-for-credentials-and-metering`
