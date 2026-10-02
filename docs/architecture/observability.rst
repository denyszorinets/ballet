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

Dashboards
----------

The single-host installation provisions Prometheus and Grafana with
three dashboards (:doc:`/how-to/install-single-host`):

- **Delivery** — per customer and project: throughput (tickets done per
  day), lead time, rework rate, stage outcomes, pipelines in progress and
  their waits, questions per day and human versus planner answers and
  wait times.
- **LLM usage** — tokens over time by project, model and token type;
  tokens per customer and project; requests and refusals.
- **Platform** — services up, jobs and dead jobs, active runs, session
  durations per stage, versions.

Per-ticket detail — every session in order with stage, duration, tokens,
outcome and report — is the *Timeline* on the ticket page in the Ballet
UI: each session with its status, outcome, duration and tokens, its stage
report, assumptions and questions, and the waits between sessions.
Cost per token is planned.

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
