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
- **Retries for infrastructure failures** (container start, network,
  provider errors) with backoff, distinct from agent-level failures.
- **Heartbeats.** Runners report their own and each session's liveness;
  missed heartbeats trigger reconciliation
  (:doc:`/architecture/integration`).
- **Stuck detection.** Sessions that exceed their time limit or stop
  producing output are terminated and retried, then escalated.

Stay within bounds
------------------

- **Budgets** in tokens or cost per ticket, per project per day and per
  customer per month. Reaching a ticket budget raises a question;
  reaching a project or customer budget pauses scheduling for that scope
  and notifies.
- **Concurrency limits** globally, per customer and per project.
- **Iteration limits** on pipeline loops.
- **Kill switch** per project and globally.

Explain what happened
---------------------

- Every session has a timeline: stage, runtime, model, duration, tokens,
  cost, tools used, outcome, report.
- A **digest** summarizes a period (e.g. overnight) per project: tickets
  completed, merged pull requests, open questions, failures, assumptions
  made, debt filed, spend.

See :doc:`/architecture/observability`.
