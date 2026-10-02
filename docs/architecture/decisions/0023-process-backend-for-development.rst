ADR-0023: Process Backend for Development
=========================================

:Status: Accepted
:Date: 2026-10-02

Context
-------

:doc:`0009-devcontainer-per-run-on-docker-or-podman` runs every agent
session in a fresh container through the Docker Engine API. Some
development environments cannot run containers: the project's own
devcontainer has no container runtime and no privileges for one (no
Docker socket, no ``CAP_SYS_ADMIN`` for nested Podman). Developers and
the agents building Ballet still need to run the whole flow — Core
queues a run, a Runner executes a coding agent against the LLM gateway,
results come back — to develop and verify features end to end.

Decision
--------

The Runner gets a second backend, **process**, selected with
``runner.backend = "process"``: each run executes as a local process
(its own process group) in a fresh temporary workspace directory, which
is removed afterwards. The environment is minimal (``PATH``, ``LANG``
and the run's spec ``env``; ``HOME`` points into the workspace), and
``image`` is ignored.

The process backend is for **development only**. It provides no
isolation: a session can read and change anything the Runner's user can.
The Runner logs a warning at start-up when it is selected, and the
default backend stays ``docker``.

Alternatives Considered
-----------------------

Docker only
~~~~~~~~~~~

Advantages:

- One execution path; isolation always.

Disadvantages:

- No end-to-end runs where containers are unavailable, including the
  environment Ballet is developed in.

Remote Docker host for development
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Real containers.

Disadvantages:

- Extra infrastructure and credentials for every developer; not always
  available.

Decision Criteria
-----------------

Ability to develop and verify end to end everywhere; safety of
production deployments; simplicity.

Rationale
---------

Both backends implement the same interface, so everything above the
backend — the Runner link, workspace preparation, adapters — is
exercised the same way. The Docker backend is verified separately in CI,
where Docker is available.

Consequences
------------

Positive
~~~~~~~~

- End-to-end development and testing without a container runtime.

Negative
~~~~~~~~

- Two backends to maintain; behaviour can differ (file paths, tools
  available on the host versus in the image).

Risks
~~~~~

- Running the process backend in production would give agents the
  Runner's privileges. Mitigated by the explicit opt-in, the start-up
  warning and documentation.

Follow-up
~~~~~~~~~

- Keep the Docker backend's integration tests in CI.

Validation
----------

The process backend's tests run everywhere; the Docker backend's
integration tests run in CI against real Docker.

References
----------

- :doc:`0009-devcontainer-per-run-on-docker-or-podman`
- :doc:`/reference/runners`
- :issue:`109`
