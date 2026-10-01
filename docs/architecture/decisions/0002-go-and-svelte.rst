ADR-0002: Go Services and a Svelte Web UI
=========================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Ballet consists of several backend services (Core, Knowledge, Runner, LLM
Gateway) that do network I/O, manage containers and processes, and
stream logs, plus a rich supervision UI (board, Gantt, live runs,
chat).

Decision
--------

- Backend services are written in **Go** (1.27), organized as a monorepo
  with one Go module per deployable service, joined by a ``go.work``
  workspace.
- The web UI is a **Svelte** single-page application (SvelteKit in static
  / SPA mode), built with Bun, served by Core or a static host.

Alternatives Considered
-----------------------

Single Go module
~~~~~~~~~~~~~~~~

Advantages:

- Simplest dependency management.

Disadvantages:

- Services share one dependency graph; Knowledge would transitively see
  container and Docker dependencies; module boundaries do not enforce
  service boundaries.

React for the UI
~~~~~~~~~~~~~~~~

Advantages:

- Larger ecosystem (e.g. Gantt components).

Disadvantages:

- More boilerplate; project owner prefers Svelte.

Decision Criteria
-----------------

Fit for concurrent I/O and container control, operational simplicity
(static binaries), boundary enforcement, team preference.

Rationale
---------

Go's standard library, concurrency model and container ecosystem fit the
Runner and gateway well and produce single static binaries. Per-service
modules make the Knowledge/Core separation physical.

Consequences
------------

Positive
~~~~~~~~

- Simple deployment; strong service boundaries.

Negative
~~~~~~~~

- Shared types between services need a deliberate home (API contracts),
  not ad-hoc cross-module imports.

Risks
~~~~~

- A Gantt component may need to be built rather than adopted in Svelte.

Follow-up
~~~~~~~~~

- API styles are decided in
  :doc:`0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`;
  contract formats are a follow-up there.

References
----------

- :doc:`/architecture/overview`
