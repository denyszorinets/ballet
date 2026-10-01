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
     RUN[Runner<br/>Go]
     GW[LLM Gateway<br/>Go]
     CDB[(Core DB<br/>SQLite)]
     KDB[(Knowledge DB<br/>SQLite)]
     CT[Devcontainers<br/>coding agents]
     GIT[(Git platform<br/>GitHub, GitLab, Forgejo, …)]
     LLM[LLM providers]
     PROM[Prometheus / Grafana]

     ENG & CUST --> UI
     UI -- OIDC login --> IDP
     UI --> CORE
     UI --> KN
     CORE --- CDB
     KN --- KDB
     CORE --> RUN
     RUN -- docker/podman API --> CT
     CT -- tracker MCP --> CORE
     CT -- knowledge MCP --> KN
     CT -- LLM API --> GW --> LLM
     CT -- git push --> GIT
     RUN -- clone --> GIT
     CORE -- forge API: PRs, state, merge --> GIT
     UI -. links .-> GIT
     CORE & GW & RUN --> PROM

Principles
----------

- **Orchestrate, don't reimplement.** Coding agents are external
  products run in containers through adapters.
- **The customer is the isolation boundary** for data, credentials and
  knowledge — enforced server-side on every request.
- **Ballet checks, agents don't self-report.** Gates are evaluated by
  Ballet from observable facts (test results, knowledge entries, review
  decisions).
- **Humans grant autonomy.** Agents may propose; only humans approve
  plans and raise a ticket's autonomy.
- **Simple first.** Single host, Docker/Podman, SQLite; rqlite,
  Kubernetes and remote workers later behind the same Runner interface.

Code organization
-----------------

A monorepo with a Go workspace (``go.work``) and one module per
deployable service, plus the Svelte UI:

.. code-block:: text

   core/        Go module — tenancy, tracker, scheduler, orchestrator,
                RBAC, skill registry, planner, tracker MCP
   knowledge/   Go module — knowledge spaces, search, lineage, MCP
   runner/      Go module — container lifecycle, agent adapters
   gateway/     Go module — LLM proxy and usage metering
   web/         Svelte UI
   docs/        this site

Inside each module, Clean Architecture boundaries apply: domain and
application code do not depend on HTTP, SQL, Docker or MCP libraries.

See :doc:`components` for responsibilities and
:doc:`decisions/index` for the decisions behind this shape.
