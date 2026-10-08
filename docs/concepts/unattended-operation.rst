Unattended Operation
====================

Ballet is designed to run for long periods without a human present. That
requires it to be safe to leave alone: it must not stall, loop, or burn
money, and it must explain what happened.

Keep going
----------

- **Independent work never waits.** A question, failure or limit stops
  only the affected ticket and what depends on it.
- **Durable state.** All pipeline state lives in the database; a
  restart of any Ballet service resumes work where it was
  (:doc:`/architecture/decisions/0016-durable-orchestration-in-the-database`).
- **Retries for infrastructure failures** (an agent gone, network,
  provider errors) with backoff, distinct from agent-level failures.
- **Heartbeats.** Agents report their own and each session's liveness;
  missed heartbeats trigger reconciliation
  (:doc:`/architecture/integration`).
- **Stuck detection.** Sessions that exceed their time limit or stop
  producing output are terminated and retried, then escalated.

Stay within bounds
------------------

- **Budgets** in tokens per ticket, per project per day and per customer
  per day (:ref:`concepts-unattended-budgets`).
- **Concurrency limits** globally, per customer and per project.
- **Iteration limits** on pipeline loops.
- **Pause and kill switch** per project and for the whole organization
  (:ref:`concepts-unattended-pause`).

.. _concepts-unattended-budgets:

Budgets
-------

Budgets bound the tokens unattended work uses, as the LLM gateway reports
them. Counted are input, output and cache-write tokens; cache reads are
not (they cost a fraction). Set them on the customer page and in the
project settings (0: no limit):

- **per ticket**, over the ticket's lifetime — the project's limit, else
  the customer's;
- **per day** (UTC) for a project, and for a customer across its
  projects.

When work runs out of budget:

- no new stage starts: a used-up **ticket** budget raises a blocking
  question ("raise the budget, then answer"); a used-up **daily** budget
  makes the flow wait (``waiting: budget``) until there is budget again —
  the next day or a raised limit — and the scheduler starts no tickets
  there;
- the **gateway refuses** LLM calls of that scope (``403
  permission_error`` "Ballet budget exhausted: …"; it rechecks at most
  every 30 seconds), so a running session stops; its stage then waits for
  budget instead of failing.

Usage today is shown next to each budget.

.. _concepts-unattended-pause:

Pause, resume and the kill switch
---------------------------------

Humans can stop autonomous work at once — for a single ticket (move it
to *Paused*), for a project (project page) or for everything
(organization admins, on the home page):

**Pause**
   No new stages start: the scheduler starts no tickets, queued runs are
   held, and a flow reaching its next stage — or its merge — waits
   (``waiting: pause``). Sessions already running finish their stage.

**Stop all runs** (kill switch)
   Pauses like above and cancels every run in progress in the scope. A
   flow whose stage was cancelled this way runs that stage again in a new
   session after the resume.

**Resume**
   Ends the pause: waiting flows continue, held runs start.

Pausing and resuming a project or the organization needs ``run.manage``
on that scope (customer admins for their projects, organization admins
for everything). Every pause, kill and resume is recorded as an event
(``control.paused``, ``control.killed``, ``control.resumed``).

Explain what happened
---------------------

- Every session has a timeline: stage, runtime, model, duration, tokens,
  cost, tools used, outcome, report.
- A **digest** summarizes a period (e.g. overnight) per project: tickets
  completed, merged pull requests, open questions, failures, assumptions
  made, debt filed, spend.

See :doc:`/architecture/observability`.
