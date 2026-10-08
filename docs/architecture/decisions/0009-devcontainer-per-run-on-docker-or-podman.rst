ADR-0009: Devcontainer per Run on Docker or Podman
==================================================

:Status: Superseded
:Date: 2026-10-01

.. note::

   Superseded by :doc:`0025-agent-fleet-runs-sessions-as-processes`: a fleet of
   long-lived agents runs sessions as processes; Ballet creates no
   containers.

Context
-------

Each ticket should be executed like a new developer joining: clean
environment, no leftover state, full onboarding. Runs execute arbitrary
code (builds, tests) from the agent and must be isolated from the host
and from each other. Initial deployments are single-host; Kubernetes is
expected later.

Decision
--------

Every run gets a **fresh container** created from the project's
devcontainer template (``devcontainer.json`` compatible). The Runner
controls containers through the **Docker Engine API**, which Podman also
serves, behind a ``ContainerBackend`` interface so a Kubernetes backend
can be added later. Containers are destroyed after the run; logs,
results and the pushed branch are what remain.

Alternatives Considered
-----------------------

Kubernetes from the start
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Scales; production-grade scheduling.

Disadvantages:

- Heavy for development and small installations.

Reused long-lived workspaces
~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Faster start.

Disadvantages:

- State leaks between tickets; defeats the fresh-developer model.

Decision Criteria
-----------------

Isolation, simplicity, path to scale.

Rationale
---------

One API covers both Docker and Podman; the backend interface keeps the
Kubernetes path open without paying for it now.

Consequences
------------

Positive
~~~~~~~~

- Reproducible runs; easy local development.

Negative
~~~~~~~~

- Container start and dependency installation add latency; templates
  should pre-bake toolchains.

Risks
~~~~~

- The Runner needs container-socket access — a privileged component that
  must be hardened.

Follow-up
~~~~~~~~~

- Kubernetes backend ADR when needed.

References
----------

- :doc:`/architecture/components`
- :doc:`/architecture/security`
