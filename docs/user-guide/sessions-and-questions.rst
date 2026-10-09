Sessions and questions
======================

Every pipeline stage is a **session** of a coding agent — Claude Code or
opencode — running on an agent of the project's pool, in a fresh
workspace on the ticket's branch. You can watch it, talk to it, and
answer its questions while it runs.

Watching a session
------------------

On the ticket page, **Agent activity** lists the ticket's sessions with
stage, runtime, duration, tokens and outcome. **Show session** opens its
transcript: what the agent says, each tool it calls and the result, and
your messages. A running session's transcript updates live.

Talking to a running session
----------------------------

Under a running session's transcript:

**Send**
   Gives the agent a message. If it is in the middle of a turn, the
   message is delivered when the turn ends — like a colleague reading
   chat after finishing a thought.
**Interrupt** / **Interrupt and send**
   Stops the current turn now; with a message, the agent continues with
   it. Use it to stop a wrong direction early.

Your messages stay in the transcript, marked *You*.

Questions
---------

Agents are instructed to make reasonable, recorded **assumptions** for
small reversible choices and to **ask** about anything irreversible,
ambiguous or outside the ticket (:doc:`/concepts/questions`).

A question first goes to the **planner**, which answers from the
knowledge base when it can, citing its sources. Questions about intent,
scope, money, credentials or anything hard to undo always go to humans:
they appear in the **Inbox** (with a count in the header), most
impactful first.

.. figure:: /_static/screenshots/inbox.png
   :alt: The inbox: a list of open questions and the selected question with an answer form.

   The inbox.

Select a question to see its ticket and context, then:

**Answer and resume**
   Records your answer; the ticket continues and the next question opens.
**Open a sub-chat**
   Discuss the question with the planner first — it knows the ticket, the
   question and the latest reports, and can research. The planner
   suggests; you answer.

Each answer is saved as a **decision** in the organization's knowledge base,
linked to the ticket, so no agent asks it again.

Answering online, or later
~~~~~~~~~~~~~~~~~~~~~~~~~~

After a **blocking** question the session waits, with its whole
context, for the project's **answer window** (15 minutes unless the
project's settings say otherwise):

- **within the window**, your answer goes straight into the same
  session, which continues as if you had been there all along;
- **after it**, the session **parks**: it pushes its work in progress as
  a ``WIP`` commit, saves its conversation and ends — nothing waits idle.
  The ticket shows *Waiting for answer*. When you answer, a new session
  of the same stage resumes the saved conversation (Claude Code) or
  starts afresh with the questions and answers (opencode).

Other tickets keep running either way; only the ticket and what depends
on it wait.

.. figure:: /_static/screenshots/session-answer.png
   :alt: A session transcript: the agent raises a blocking question, ends its turn, and receives the human's answer as its next message.

   An answer delivered into the running session (recorded with the fake
   model, which only echoes tool results).

Assumptions
-----------

**Assumptions** on the project page lists what agents assumed instead of
asking. **Confirm** the right ones; **Reject…** the wrong ones and say
what is right. While the ticket is open, the correction goes to its next
sessions; once it is done, Ballet proposes a bug ticket as a changeset.
