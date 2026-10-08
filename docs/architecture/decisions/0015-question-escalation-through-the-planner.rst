ADR-0015: Question Escalation Through the Planner
=================================================

:Status: Superseded
:Date: 2026-10-01

.. note::

   Superseded by :doc:`0026-interactive-sessions-park-when-idle`: sessions
   wait online for answers and park after a window; planner-first
   answering and assume-or-ask are kept.

Context
-------

Humans should take part in execution only when an agent has a question.
Many questions agents ask are already answered somewhere — in the
documentation, an earlier decision, or the plan discussion. Every
question that reaches a human costs waiting time, often hours overnight.
Blocked sessions must not hold containers while waiting.

Decision
--------

- Agents follow an **assume-or-ask** rule (encoded in skills): record an
  assumption for reversible, low-impact uncertainty; ask a question for
  irreversible, high-impact or ambiguous ones.
- Asking ends the session after pushing work in progress; the ticket
  enters *Waiting for answer*.
- Questions go **first to the planner agent**, which answers only from
  cited sources (knowledge base, plan, documentation). Otherwise the
  question goes to the human inbox in the Ballet UI.
- Humans answer in a **sub-chat** with the planner scoped to the ticket.
- Answers are written to the knowledge base, and a new session of the
  same stage continues with the question and answer in context.

Alternatives Considered
-----------------------

All questions go to humans
~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest; no risk of the planner answering wrongly.

Disadvantages:

- Many avoidable interruptions and overnight stalls.

Keep the session alive while waiting
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- The asking agent keeps its full context.

Disadvantages:

- Containers and sessions idle for hours; fragile across restarts.

Decision Criteria
-----------------

Human time, waiting time, robustness, answer correctness.

Rationale
---------

The planner holds the project's context and can resolve questions that
documentation already answers; requiring citations limits invented
answers. Ending sessions makes waiting free and restart-safe; good stage
reports and knowledge make resumption cheap.

Consequences
------------

Positive
~~~~~~~~

- Fewer human interruptions; knowledge grows with every answer.

Negative
~~~~~~~~

- A resumed session must re-establish context.

Risks
~~~~~

- The planner may answer confidently but wrongly; every planner answer
  is visible in the digest and can be overruled.

Follow-up
~~~~~~~~~

- Notification channels (email, chat, push), configurable per user and
  project; the UI inbox is the only channel at first.
- Measure share of questions answered by the planner.

References
----------

- :doc:`/concepts/questions`
- :doc:`0020-planner-runs-in-process-in-core`
