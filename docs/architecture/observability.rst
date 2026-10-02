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
   Counters such as ``ballet_llm_tokens_total`` with labels
   ``customer``, ``project``, ``model`` and ``token_type``
   (:doc:`/reference/metrics`); cost per token is planned.

Ticket and run IDs are **not** metric labels: each would create
unbounded time series. Grafana uses Prometheus for customer/project
trends; per-ticket breakdowns come from Core (Ballet UI, and the REST
API through a Grafana JSON data source).

Pipeline and delivery metrics
-----------------------------

Ballet must show not only what it spends but whether it delivers good
software. Core counts delivery from its event log — stages and their
outcomes, pipeline lead time and rework (iterations), agent sessions and
their duration, questions and how long they wait, waits for humans,
pauses and budgets — and reports the current state (active pipelines,
open questions, active runs) at each scrape. Labels are ``customer``,
``project``, ``stage`` and small enumerations, never ticket IDs. The
metrics are listed in :doc:`/reference/metrics`.

Per-ticket detail (each session's timeline, report, tokens, cost) is in
the Core database.

Planned dashboards
------------------

- **Customer:** tokens and cost over time, by project and model.
- **Project:** throughput (tickets done per day), lead time, cost per
  ticket / epic / milestone, rework rate, stage pass rates, questions
  per ticket and human wait time.
- **Ticket:** every session in order — stage, duration, tokens, cost,
  outcome, report. This is the *Timeline* on the ticket page in the
  Ballet UI: each session with its status, outcome, duration and tokens,
  its stage report, assumptions and questions, and the waits between
  sessions.
- **Platform:** active sessions, queue depth, container and provider
  failures, retries, budget stops.

Digest
------

For any period (typically overnight), Ballet produces a per-project
digest (project page → *Digest*, or ``GET
/api/v1/projects/{project}/digest``). It is computed on request from the
project's events in the period and its current state:

- tickets done (by their pipeline or by hand), failed, started, and pull
  requests merged;
- agent sessions by status and the counted tokens used;
- questions raised and who answered them (planner or humans), and the
  questions open now, blocking ones first;
- assumptions recorded, changesets proposed;
- pipelines waiting now and for what;
- interventions: pauses, kill switches and resumes.

The page offers the last 12 hours to 30 days and downloads the digest as
Markdown. Delivery through notification channels comes later.

Operational metrics
-------------------

Services expose Prometheus metrics, structured logs (``log/slog``, JSON)
and OpenTelemetry traces where useful. Session logs (agent output) are
stored with the run and streamed to the UI.
