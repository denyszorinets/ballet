Architecture Overview
=====================

System context
--------------

.. mermaid::

   flowchart LR
     subgraph Humans
       ENG[Engineers]
       CUST[Customer staff]
     end
     IDP[OIDC identity provider]
     UI[Web UI<br/>Svelte]
     CORE[Core<br/>Go]
     KN[Knowledge<br/>Go]
     AG[Agents × N<br/>Go, in containers or VMs]
     GW[LLM Gateway<br/>Go]
     CDB[(Core DB<br/>SQLite)]
     KDB[(Knowledge DB<br/>SQLite)]
     CT[Sessions<br/>Claude Code, opencode]
     GIT[(Git platform<br/>GitHub, GitLab, Forgejo, …)]
     LLM[LLM providers]
     PROM[Prometheus / Grafana]

     ENG & CUST --> UI
     UI -- OIDC login --> IDP
     UI --> CORE
     UI --> KN
     CORE --- CDB
     KN --- KDB
     AG -- WebSocket: runs, events, input --> CORE
     AG -- processes --> CT
     CT -- tracker MCP --> CORE
     CT -- knowledge MCP --> KN
     CT -- LLM API --> GW --> LLM
     CT -- git push --> GIT
     CT -- clone --> GIT
     CORE -- forge API: PRs, state, merge --> GIT
     UI -. links .-> GIT
     CORE & GW & AG --> PROM

Principles
----------

- **Orchestrate, don't reimplement.** Coding agents are external
  products, driven by Ballet's agents through drivers.
- **The customer is the isolation boundary** for data, credentials and
  knowledge — enforced server-side on every request.
- **Ballet checks, agents don't self-report.** Gates are evaluated by
  Ballet from observable facts (test results, knowledge entries, review
  decisions).
- **Humans grant autonomy.** Agents may propose; only humans approve
  plans and raise a ticket's autonomy.
- **Simple first.** SQLite (rqlite later); Ballet manages no
  containers: a fleet of agents dials in — N containers on one host, a
  Kubernetes Deployment, or VMs
  (:doc:`decisions/0025-agent-fleet-runs-sessions-as-processes`).

Code organization
-----------------

A monorepo with a Go workspace (``go.work``) and one module per
deployable service, plus the Svelte UI:

.. code-block:: text

   kit/         Go module — shared operational plumbing (no domain logic)
   core/        Go module — tenancy, tracker, scheduler, orchestrator,
                RBAC, skill registry, planner, tracker MCP
   knowledge/   Go module — knowledge spaces, search, lineage, MCP
   agent/       Go module — the agent: sessions as processes, runtime drivers
   gateway/     Go module — LLM proxy and usage metering
   web/         Svelte UI
   docs/        this site

Inside each module, Clean Architecture boundaries apply: domain and
application code do not depend on HTTP, SQL or MCP libraries.

See :doc:`components` for responsibilities and
:doc:`decisions/index` for the decisions behind this shape.
