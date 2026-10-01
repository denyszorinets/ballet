ADR-0013: Planner Runs as a Containerized Agent
===============================================

:Status: Rejected
:Date: 2026-10-01

.. note::

   Rejected in favour of :doc:`0020-planner-runs-in-process-in-core`
   to keep the planner simple.

Context
-------

Humans plan work by chatting with a planner agent that writes
documentation, reads lineage, and proposes plan changesets. Ballet's
principle is to orchestrate existing agents rather than build one
(:doc:`0003-orchestrate-existing-coding-agents`). The planner needs the
tracker and knowledge MCPs, the project's skills, and possibly read
access to the code.

Decision
--------

The planner is an agent runtime session (same adapters as worker runs)
in a **long-lived, per-project planning container** with a read-only
checkout of the repositories, the tracker and knowledge MCPs, and a
planning skill set. Core relays the conversation between the UI chat and
the session (headless, streaming mode), and persists the transcript.

The planner can change the plan only by submitting plan changesets for
human approval.

Alternatives Considered
-----------------------

Ballet-native planner loop on an LLM API
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Fine-grained control of chat UX, tools and streaming.

Disadvantages:

- Builds and maintains an agent — what Ballet chose not to do.

Decision Criteria
-----------------

Consistency with ADR-0003, chat UX quality, implementation effort.

Rationale
---------

Reusing runtimes and adapters keeps one execution model for planner and
workers and gives the planner the same code and skill access.

Consequences
------------

Positive
~~~~~~~~

- Planner improves as runtimes improve; skills shape planning behavior.

Negative
~~~~~~~~

- Chat UX is limited by what the runtime's headless streaming mode
  exposes.

Risks
~~~~~

- Long-lived sessions may hit context limits; needs session
  summarization or restart with a knowledge-based recap.

Follow-up
~~~~~~~~~

- Spike: Claude Code headless multi-turn session bridged to a web chat.

Validation
----------

The spike above; if the UX is inadequate, revisit with a native loop.

References
----------

- :doc:`/concepts/workflow`
