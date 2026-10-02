Questions and Human Involvement
===============================

During execution, humans are involved only when an agent needs
something it cannot work out itself. Ballet's job is to make that rare,
fast and never a reason for other work to stop.

Assume or ask
-------------

Skills instruct agents to classify every uncertainty:

**Reversible, low-impact** — make a reasonable assumption
   Record it and continue. Assumptions are listed on the ticket and in
   the project's assumption register, where a human confirms or rejects
   them (see below).

**Irreversible, high-impact or genuinely ambiguous** — ask a question
   Examples: product behavior not covered by any document, a choice that
   changes a public API or data model, conflicting requirements, missing
   access or credentials.

.. _concepts-questions-routing:

Asking a question
-----------------

Any stage — and the planner — can raise a question through the tracker
MCP: the question, context, and optionally suggested answers.

#. The session pushes its work in progress and ends; its container is
   released. Nothing waits idle overnight.
#. The ticket moves to **Waiting for answer**. Work that does not depend
   on it continues; dependent tickets wait.
#. Ballet first routes the question to the **planner agent**. In a short,
   unattended conversation it may search the knowledge base and read the
   project's tracker items, and must either answer **citing its sources**
   (an answer without sources is refused) or escalate. Questions about
   product intent, scope, priorities, money, credentials or anything hard
   to undo are always escalated. The attempt is metered to the ticket.
#. If the planner escalates (or fails), the question goes to the **human
   inbox**; any project member with ``tracker.write`` can answer it
   (``POST /api/v1/questions/{question}/answer``).
#. Once no blocking question of the ticket is open, Ballet starts a new
   session of the same stage with the questions and answers handed over,
   and the pipeline continues.
#. The answer is written to the knowledge base as a **decision** entry
   linked to the ticket, authored by whoever answered, so the same
   question is not asked again.

Questions Ballet raises itself — the iteration limit, a stuck stage — go
to the humans directly. After the answer, a flow stopped by the
iteration limit continues where its loop was going, with a fresh loop
budget.

.. _concepts-questions-assumptions:

The assumption register
-----------------------

Every project has an **assumption register** (project page →
*Assumptions*): the assumptions its agents recorded, newest first,
filtered by review. Anyone with ``tracker.write`` can **confirm** an
assumption or **reject** it, saying what is right instead. Agents see the
review in ``ticket_context``. A rejection is recorded
(``item.assumption_reviewed``) and creates follow-up work:

- while the ticket is not done or cancelled, an answered question on the
  ticket ("Assumption rejected: …" with the human's correction), which its
  next sessions receive in their context;
- once the ticket is resolved, a **changeset** proposing a bug ticket that
  corrects the assumption, related to the original ticket, for a human to
  approve.

The inbox and sub-chats
-----------------------

The **Inbox** (main navigation, with the number of questions waiting for
a human) lists the open questions of every project you can read, most
impactful first: questions waiting for a human before those the planner
is still working on, blocking ones first, then by how many unresolved
items wait behind the ticket in the dependency graph, then the oldest.

Selecting a question shows its ticket, text and context, and an answer
form (for members with ``tracker.write``): **Answer and resume** records
the answer and resumes the ticket, and the next question opens. **Open a
sub-chat** starts a conversation with the planner scoped to the question
— its instructions hold the ticket, the question and the latest stage
reports, and it can research the knowledge base and the plan. The
planner suggests; the human answers. There is one sub-chat per question,
shared by everyone who opens it; sub-chats do not appear among the
project's planner sessions. Switching between sub-chats is meant to
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
