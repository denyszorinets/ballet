Code Layout
===========

Ballet is a monorepo. Backend services are separate Go modules joined by
a Go workspace (:doc:`/architecture/decisions/0002-go-and-svelte`).

.. code-block:: text

   go.work            workspace: core, gateway, kit, knowledge, runner
   core/              Core service module
   gateway/           LLM gateway module
   knowledge/         Knowledge service module
   runner/            Runner module
   kit/               shared library module (no business logic)
   web/               Svelte UI
   docs/              this documentation (uv project)

Modules
-------

``core``, ``gateway``, ``knowledge``, ``runner``
   One deployable service each, module path
   ``github.com/denyszorinets/ballet/<name>``. A service module never
   imports another service module; services talk over their APIs.

``kit``
   Operational plumbing shared by all services: configuration loading
   (``kit/config``), JSON logging (``kit/logging``), health and readiness
   (``kit/health``), graceful HTTP serving (``kit/server``), and the
   ``kit/service`` wiring that combines them with Prometheus metrics.
   ``kit`` contains no Ballet domain concepts.

Each service module declares
``replace github.com/denyszorinets/ballet/kit => ../kit`` so that it is
also a valid standalone module. Verify standalone builds with:

.. code-block:: bash

   cd core && GOWORK=off go build ./...

Inside a service
----------------

Packages follow Clean Architecture boundaries; dependencies point
inward.

.. code-block:: text

   <service>/
     cmd/<service>/        entry point: configuration and wiring only
     internal/domain/      entities, value objects, invariants
     internal/app/         use cases; interfaces (ports) for what they need
     internal/infra/       adapters: SQLite, Docker, forge and LLM clients
     internal/transport/   REST, WebSocket and MCP handlers

- ``domain`` imports nothing from ``app``, ``infra`` or ``transport``.
- ``app`` depends on ``domain`` and on interfaces it defines itself.
- ``infra`` and ``transport`` depend on ``app`` and ``domain``.
- Packages are created when there is code for them — no empty
  placeholders.

Services and ports
------------------

.. list-table::
   :header-rows: 1

   * - Service
     - Default address
     - Endpoints
   * - core
     - ``:8080``
     - ``/healthz`` ``/readyz`` ``/metrics``
   * - knowledge
     - ``:8081``
     - ``/healthz`` ``/readyz`` ``/metrics``
   * - gateway
     - ``:8082``
     - ``/healthz`` ``/readyz`` ``/metrics``
   * - runner
     - ``:8083``
     - ``/healthz`` ``/readyz`` ``/metrics``

Run a service locally:

.. code-block:: bash

   go run ./core/cmd/core
   curl localhost:8080/healthz

Each ``main.go`` defines a ``serviceConfig`` struct embedding
``service.Config`` and adds service-specific sections to it; see
:doc:`/reference/configuration`.
