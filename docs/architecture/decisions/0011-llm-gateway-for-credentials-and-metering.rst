ADR-0011: LLM Gateway for Credentials and Usage Metering
========================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

LLM credentials are configured per customer with optional per-project
override. Token usage must be counted per ticket and shown in dashboards
per customer, project and ticket (Grafana). Runs execute untrusted code
in containers, so raw provider keys inside containers could leak.
Different agent runtimes report usage differently, if at all.

Decision
--------

All LLM traffic from runs and planner sessions goes through a Ballet
**LLM gateway**:

- Containers receive the gateway URL and their run token instead of a
  provider key.
- The gateway resolves the credential (project override, else customer
  default), forwards the request to the provider, and records usage
  (input, output, cache tokens, model, cost) attributed to the run.
- Usage records are stored in the Core database (source of truth); the
  gateway exports Prometheus counters labelled by customer, project,
  runtime, model and token type — never by ticket or run ID.
- Grafana uses Prometheus for trends; per-ticket breakdowns are served
  by Core (UI and REST API, usable from Grafana through a JSON data
  source).
- The gateway also serves embedding requests for vector search
  (:doc:`0021-hybrid-vector-search-over-all-content`), metered the same
  way.

Alternatives Considered
-----------------------

Provider keys in containers; usage from runtime telemetry
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No proxy in the request path.

Disadvantages:

- Keys exposed to untrusted code; telemetry format differs per runtime
  and may be incomplete.

Ticket ID as a Prometheus label
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Everything in one data source.

Disadvantages:

- Unbounded cardinality degrades Prometheus over time.

Decision Criteria
-----------------

Security of credentials, uniform metering across runtimes, metric
cardinality, operational complexity.

Rationale
---------

One choke point gives key isolation and runtime-independent accounting
at the cost of one extra service.

Consequences
------------

Positive
~~~~~~~~

- Accurate per-ticket cost; easy per-customer budgets and limits later.

Negative
~~~~~~~~

- Gateway must speak each provider's API (Anthropic, OpenAI-compatible),
  including streaming.

Risks
~~~~~

- Gateway availability gates all runs; keep it simple and stateless.
- Subscription-based agent login (not API keys) cannot be proxied this
  way.

Follow-up
~~~~~~~~~

- Verify each runtime supports a custom base URL.

Validation
----------

Proof of concept: Claude Code run through the gateway with streaming;
usage records match provider-reported usage.

References
----------

- :doc:`/architecture/observability`
- :doc:`/architecture/security`
