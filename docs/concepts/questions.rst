Questions and Human Involvement
===============================

During execution, humans are involved only when an agent needs
something it cannot work out itself. Ballet's job is to make that rare,
fast and never a reason for other work to stop.

Assume or ask
-------------

Skills instruct agents to classify every uncertainty:

**Reversible, low-impact** — make a reasonable assumption
   Record it and continue. Assumptions are listed on the ticket and in the stage report. A human
   can confirm or correct them later; a correction becomes a new ticket.

**Irreversible, high-impact or genuinely ambiguous** — ask a question
   Examples: product behavior not covered by any document, a choice that
   changes a public API or data model, conflicting requirements, missing
   access or credentials.

Asking a question
-----------------

Any stage — and the planner — can raise a question through the tracker
MCP: the question, context, and optionally suggested answers.

#. The session pushes its work in progress and ends; its container is
   released. Nothing waits idle overnight.
#. The ticket moves to **Waiting for answer**. Work that does not depend
   on it continues; dependent tickets wait.
#. Ballet first routes the question to the **planner agent**, which
   answers it if the knowledge base, plan or documentation already does,
   citing its sources.
#. If the planner cannot answer, the question goes to the **human
   inbox** in the Ballet UI.
#. Once answered, Ballet starts a new session of the same stage with the
   question and answer in its context, and the pipeline continues.
#. The answer is written to the knowledge base as a decision or note, so
   the same question is not asked again.

The inbox and sub-chats
-----------------------

The UI inbox lists open questions, ordered by **impact**: how much work
is blocked behind each question, computed from the dependency graph.

Opening a question opens a **sub-chat** scoped to that ticket: a short
conversation with the planner agent, which has the ticket, the question,
the stage reports and the relevant knowledge. The human can ask for
clarification, discuss options, then confirm an answer. Closing the
sub-chat releases the ticket. Switching between sub-chats is meant to
take seconds — answering a night's worth of questions should take
minutes.

Notifications
-------------

Initially, the Ballet UI is the only channel: the inbox, unread badges
and the digest. Notification channels (email, chat, push) are a
configurable, later addition per user and project.

Other reasons a human is asked
------------------------------

- Iteration limit reached in the pipeline (:doc:`pipeline`).
- Budget exhausted (:doc:`unattended-operation`).
- Repeated infrastructure failures that retries did not fix.
- Review mode requires human review for this ticket.

See :doc:`/architecture/decisions/0015-question-escalation-through-the-planner`.
