ADR-0003: Orchestrate Existing Coding Agents Through Runtime Adapters
=====================================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Ballet's purpose is coordinating AI software development: ordering work,
supplying context, enforcing process, and keeping humans in control.
Capable coding agents already exist (Claude Code, Codex, opencode) and
evolve faster than Ballet could match. Different customers may prefer or
require different agents and providers.

Decision
--------

Ballet does **not** implement a coding agent. It runs existing agents
headless inside devcontainers through **agent runtime adapters**. An
adapter knows, for one runtime:

- how to install skills in the runtime's expected layout;
- how to configure MCP servers (tracker, knowledge);
- how to point the runtime at the LLM gateway;
- how to start it non-interactively with the onboarding prompt;
- how to detect completion and collect the result.

The runtime is part of a ticket's execution policy, defaulted by the
project's process profile. Claude Code is the first adapter.

Alternatives Considered
-----------------------

Build a Ballet agent on an LLM API
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Full control over the loop, tools and telemetry.

Disadvantages:

- Re-implements a fast-moving product category; permanently behind.

Support a single runtime only
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest.

Disadvantages:

- Customer lock-in; no way to compare runtimes.

Decision Criteria
-----------------

Functional fit, maintainability, customer flexibility, reversibility.

Rationale
---------

Ballet's value is in what surrounds the agent. Adapters keep the
integration surface small and let Ballet benefit from agent improvements
for free.

Consequences
------------

Positive
~~~~~~~~

- New runtimes are added by writing an adapter.

Negative
~~~~~~~~

- Ballet depends on each runtime's headless mode and configuration
  formats, which may change.

Risks
~~~~~

- Runtimes differ in skill/MCP support; the lowest common denominator
  may limit features.

Follow-up
~~~~~~~~~

- Define the adapter interface; implement Claude Code; add a second
  runtime early to validate the abstraction.

References
----------

- :doc:`/concepts/workflow`
- :doc:`0011-llm-gateway-for-credentials-and-metering`
