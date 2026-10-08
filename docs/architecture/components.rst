Components
==========

Core
----

The system of record for everything except knowledge.

- Tenancy: organization, customers, projects, process profiles.
- Tracker: milestones, epics, tickets, dependencies (DAG validation),
  plan changesets, pull request records, gates.
- Scheduler: ready queue, concurrency limits, Gantt forecast.
- Pipeline orchestrator: durable per-ticket stage state machine, stage
  reports, iteration limits, retries, budgets
  (:doc:`decisions/0016-durable-orchestration-in-the-database`).
- Questions: routing to the planner, human inbox, digest (notification
  channels later).
- Identity and authorization: OIDC token validation, claim → role
  mapping, run-token issuance (:doc:`security`).
- Skill registry (database only) and devcontainer templates.
- Hybrid search over items and skills (stage reports and answers when
  they exist): an indexer tails the event log from a persisted cursor
  and keeps documents and embeddings current; results are filtered by
  RBAC.
- Usage records per run (fed by the LLM gateway).
- **Forge adapters** (GitHub, Forgejo/Gitea, GitLab, Bitbucket): open
  pull requests, receive webhooks or poll for review/CI/merge state,
  fetch review comments, post agent reviews, merge on ``auto`` policy
  (:doc:`decisions/0007-review-on-git-platforms-via-forge-adapters`).
- **Tracker MCP** for agents; REST for stateless and WebSocket JSON-RPC
  for stateful traffic with the UI and agents (:doc:`integration`).

Knowledge
---------

Separate service, separate database
(:doc:`decisions/0005-knowledge-as-separate-service-with-mcp`).

- Customer-scoped spaces; entries (documents, decisions, notes, debt)
  with immutable version history and links to projects and tracker
  items.
- Humans reach it through Core, which authorizes and forwards with a
  request-scoped token
  (:doc:`decisions/0022-humans-reach-knowledge-through-core`).
- Hybrid full-text + vector search; lineage graph queries
  (:doc:`decisions/0021-hybrid-vector-search-over-all-content`).
- **Knowledge MCP** for agents; REST API for the UI editor.
- Trusts Core-issued tokens for identity and customer scope; holds no
  tracker data beyond entity IDs.

Agent
-----

Executes runs (:doc:`decisions/0025-agent-fleet-runs-sessions-as-processes`,
:doc:`/reference/agents`). A long-lived process, one per container or
VM; a fleet is N of them — Ballet creates no containers.

- Dials Core over WebSocket JSON-RPC with its token and pool label;
  heartbeats report agent and per-session liveness.
- Runs each session as a process group in a fresh workspace, as an
  unprivileged session user, and removes the workspace afterwards.
- Prepares the workspace: clones the repository and creates the run's
  branch; the coding agent pushes it.
- **Drivers** — one per runtime (Claude Code, opencode): skills, MCP
  configuration, the gateway, the session protocol; they normalize the
  session's output into events and keep it open between turns, so
  humans can talk to it and it can wait for answers, park and resume
  (:doc:`decisions/0026-interactive-sessions-park-when-idle`).
- Toolchains come from the project's devcontainer with the ballet-agent
  Feature (:doc:`/how-to/agent-pools`); Core routes runs by pool.

LLM Gateway
-----------

A proxy between agent sessions and LLM providers
(:doc:`decisions/0011-llm-gateway-for-credentials-and-metering`).

- Authenticates the run token, injects the customer's (or project's)
  provider credentials — sessions never see real keys.
- Meters tokens per request and attributes them to run → ticket →
  project → customer.
- Exposes usage metrics (:doc:`observability`).

Planner
-------

The planner is a conversation loop inside the Core process, one per chat
or sub-chat. It calls the LLM through the gateway and uses Core's use
cases and the Knowledge client directly as tools; its output streams to
the UI over WebSocket
(:doc:`decisions/0020-planner-runs-in-process-in-core`).

Web UI
------

Svelte single-page application:

- planner chat and plan-changeset approval;
- question inbox ordered by impact, with per-ticket sub-chats;
- digest of what happened while nobody was watching;
- board, ticket detail, Gantt chart;
- live session view per pipeline stage (logs, reports, pause, cancel,
  retry);
- pull request status per ticket and run, with links to the git
  platform;
- knowledge browser and editor, lineage view;
- skill registry, process profiles, admin and role bindings.
