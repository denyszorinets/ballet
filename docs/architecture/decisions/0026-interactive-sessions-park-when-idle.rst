ADR-0026: Interactive Sessions That Park When Idle
==================================================

:Status: Accepted
:Date: 2026-10-08

Context
-------

Under :doc:`0015-question-escalation-through-the-planner` an agent that
asks a question ends its session; a new session continues after the
answer, with the question and answer in its prompt. The asking agent
loses its context, and nobody can steer a session while it runs.

Humans want to watch a running session, send it messages, interrupt it
and answer its questions while it is still running. With a fixed fleet
of agents (:doc:`0025-agent-fleet-runs-sessions-as-processes`), sessions
must not hold an agent for hours while nobody answers.

Decision
--------

Sessions are **interactive**, and **park** when they wait too long.

- **Streaming drivers.** The agent drives each runtime through its
  streaming interface (Claude Code: ``--input-format stream-json
  --output-format stream-json``; opencode: ``opencode serve``) and keeps
  the session open between turns.
- **Normalized events.** The agent sends Core one event stream per
  session: assistant text, tool calls and results, questions, cost and
  state changes. Core stores it, so the UI can reconnect and replay, and
  a parked session's history stays visible.
- **Human input.** From the UI, through Core: a *message* (delivered at
  the next turn), an *interrupt* (stop the current turn, then deliver)
  and an *answer* (to a question).
- **Questions.** The agent asks through a Ballet MCP tool that returns
  at once; skills tell it to end its turn after asking, and the answer
  arrives as the next user message. The planner tries first, from cited
  sources; otherwise the question goes to the human inbox. The
  assume-or-ask rule stays.
- **Park after a window.** A session waiting for input longer than the
  project's *online window* (with a Ballet default) is parked: the agent
  pushes work in progress, uploads the runtime's session state to Core
  and frees itself. The ticket enters *Waiting for answer*.
- **Resume anywhere.** When the answer comes, any agent of the pool
  clones the branch, restores the session state and continues the same
  session with full context.
- Parking also drains agents for restarts and updates.

This supersedes :doc:`0015-question-escalation-through-the-planner`;
its planner-first answering and assume-or-ask rule are kept.

Alternatives Considered
-----------------------

End the session on every question (ADR-0015)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest; nothing waits.

Disadvantages:

- No online answers or steering; context is lost on every question.

Keep sessions alive until answered
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No session state to save and restore.

Disadvantages:

- Overnight questions block agents; sessions are lost on restarts.

Blocking question tool
~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- The agent's turn simply continues with the answer.

Disadvantages:

- Long-running MCP calls hit runtime timeouts and cannot survive
  parking.

Decision Criteria
-----------------

Human experience, agent context, fleet utilisation, robustness to
restarts, complexity.

Rationale
---------

Most questions are answered within minutes when someone is around, and
then the session should simply continue. Parking keeps the fleet free
the rest of the time, and restoring the runtime's own session state
keeps context better than rebuilding it from a prompt. A question tool
that returns at once works the same in every runtime.

Consequences
------------

Positive
~~~~~~~~

- Humans can watch, steer and answer running sessions.
- Answered questions keep the session's context, also after parking.
- Restarts and updates lose no work.

Negative
~~~~~~~~

- Drivers depend on each runtime's streaming interface and session
  storage format.
- Core stores event streams and session state.

Risks
~~~~~

- A runtime may not support restoring a session; its driver then
  starts a new session with the question and answer in the prompt, as
  under ADR-0015.
- Session state can hold secrets the session saw; store it like other
  run data and delete it with the ticket.

Follow-up
~~~~~~~~~

- Mark :doc:`0015-question-escalation-through-the-planner` superseded
  (done).
- Update :doc:`/concepts/questions` as the implementation lands.

References
----------

- :doc:`0015-question-escalation-through-the-planner`
- :doc:`0025-agent-fleet-runs-sessions-as-processes`
- :doc:`/concepts/questions`
- :issue:`160`
