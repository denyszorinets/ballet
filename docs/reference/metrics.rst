Metrics
=======

Every Ballet service exposes Prometheus metrics on ``/metrics`` (see
:doc:`/architecture/observability`). Labels are organization and project
**keys**, stage IDs and small enumerations — never ticket or run IDs.

All services
------------

``ballet_build_info{service, version, …}`` (gauge, always 1)
   Build information.

Core — delivery
---------------

Counted from Core's event log as it happens (counters start at zero when
Core starts):

``ballet_stages_finished_total{organization, project, stage, outcome}``
   Pipeline stages finished; ``outcome`` is ``done``, ``failed`` or
   ``blocked``.

``ballet_flows_finished_total{organization, project, status}``
   Ticket pipelines finished: ``done``, ``failed`` or ``stopped``.

``ballet_flow_lead_time_seconds{organization, project, status}`` (histogram)
   Time from a pipeline's start to its end.

``ballet_flow_iterations{organization, project}`` (histogram)
   Loops a completed pipeline went through (rework).

``ballet_flow_waits_total{organization, project, reason}``
   Times pipelines started waiting: ``approval``, ``checks``, ``review``,
   ``merge``, ``answer``, ``pause`` or ``budget``.

``ballet_runs_finished_total{organization, project, stage, status}``
   Agent sessions finished: ``succeeded``, ``failed`` or ``cancelled``.

``ballet_run_duration_seconds{organization, project, stage}`` (histogram)
   Duration of agent sessions.

``ballet_questions_raised_total{organization, project, blocking}``
   Questions raised by agents and by Ballet.

``ballet_questions_answered_total{organization, project, by}`` and ``ballet_question_answer_seconds{organization, project, by}`` (histogram)
   Questions answered by the ``planner`` or a ``human``, and how long
   they waited.

``ballet_usage_tokens_total{organization, project, type}``
   LLM tokens Core stored from the gateway's reports (``input``,
   ``output``, ``cache_read``, ``cache_write``) — the data budgets count.

Current state, read from the database at each scrape:

``ballet_flows_active{organization, project, status, waiting}`` (gauge)
   Pipelines in progress; ``waiting`` says what a waiting one waits for.

``ballet_questions_open{organization, project, route}`` (gauge)
   Open questions with the ``planner`` or the ``human`` inbox.

``ballet_runs_active{organization, project, status}`` (gauge)
   Agent runs ``queued``, ``starting`` or ``running``.

Core — platform
---------------

``ballet_jobs_total{kind, result}``
   Durable jobs executed: ``done``, ``retry`` or ``dead``.

``ballet_planner_compactions_total``
   Planner conversations compacted.

LLM gateway
-----------

``ballet_llm_requests_total{organization, project, model, status}``
   LLM requests by HTTP status.

``ballet_llm_tokens_total{organization, project, model, token_type}``
   LLM tokens as the gateway measured them, live.
