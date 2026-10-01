Observability
=============

Token usage and cost
--------------------

Every LLM request from a run passes through the LLM gateway, which
records input, output and cache tokens and the model
(:doc:`/reference/llm-gateway`). Cost is computed from tokens and a
price table (planned).

Two stores, by purpose:

**Usage records (Core database) — source of truth.**
   One row per request or per run aggregate, attributed to run, ticket,
   epic, project and customer. Used for per-ticket analysis, customer
   billing and in-app usage views.

**Metrics (Prometheus) — trends and alerting.**
   Counters such as ``ballet_llm_tokens_total`` and
   ``ballet_llm_cost_total`` with labels ``customer``, ``project``,
   ``runtime``, ``model``, ``token_type``.

Ticket and run IDs are **not** metric labels: each would create
unbounded time series. Grafana uses Prometheus for customer/project
trends; per-ticket breakdowns come from Core (Ballet UI, and the REST
API through a Grafana JSON data source).

Pipeline and delivery metrics
-----------------------------

Ballet must show not only what it spends but whether it delivers good
software. Prometheus metrics (labels ``customer``, ``project``,
``stage``, ``runtime``, ``model``, ``outcome`` — never ticket IDs):

- ``ballet_stage_runs_total`` — stage sessions by outcome (pass, returned,
  failed, question, timeout).
- ``ballet_stage_duration_seconds`` — histogram per stage.
- ``ballet_ticket_lead_time_seconds`` — Ready to Done.
- ``ballet_ticket_iterations`` — pipeline round trips per completed
  ticket (rework rate).
- ``ballet_questions_total`` and ``ballet_question_wait_seconds`` — how
  often humans are needed and how long work waits for them.
- ``ballet_questions_answered_by_planner_total`` — questions resolved
  without a human.
- ``ballet_ready_queue_depth``, ``ballet_active_sessions``.
- ``ballet_budget_exhausted_total``.

Per-ticket detail (each session's timeline, report, tokens, cost) is in
the Core database.

Planned dashboards
------------------

- **Customer:** tokens and cost over time, by project and model.
- **Project:** throughput (tickets done per day), lead time, cost per
  ticket / epic / milestone, rework rate, stage pass rates, questions
  per ticket and human wait time.
- **Ticket:** every session in order — stage, duration, tokens, cost,
  outcome, report.
- **Platform:** active sessions, queue depth, container and provider
  failures, retries, budget stops.

Digest
------

For any period (typically overnight), Ballet produces a per-project
digest from the same data: tickets completed and merged, open questions
by impact, failures, assumptions made, debt filed, spend versus budget.
It is shown in the UI; delivery through notification channels comes
later.

Operational metrics
-------------------

Services expose Prometheus metrics, structured logs (``log/slog``, JSON)
and OpenTelemetry traces where useful. Session logs (agent output) are
stored with the run and streamed to the UI.
