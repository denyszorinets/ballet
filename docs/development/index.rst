Development
===========

Contributor workflow for Ballet itself.

.. toctree::
   :maxdepth: 1

   code-layout
   persistence
   rest-api
   web
   website

Workflow
--------

- Work is tracked as tickets; every branch is
  ``feature/<ticket>_<short_title>`` from ``develop``; changes reach
  ``develop`` through reviewed pull requests; ``main`` is the release
  branch.
- New behavior is developed test-first (Go, testify, table-driven tests).
- Documentation is updated in the same change as the behavior it
  describes.

Make targets
------------

The Makefile is the entry point for all routine tasks; CI runs the same
targets. ``make help`` lists them all.

.. list-table::
   :header-rows: 1

   * - Target
     - Purpose
   * - ``make check``
     - Everything CI checks for Go and web: gofmt, vet, staticcheck,
       race tests, standalone module tests, build, web lint/type
       check/unit tests
   * - ``make test`` / ``make test-race``
     - Go tests in every module (with the race detector)
   * - ``make test-standalone``
     - Go tests per module with ``GOWORK=off`` (catches hidden workspace
       dependencies)
   * - ``make format`` / ``make format-check``
     - gofmt (rewrite / verify)
   * - ``make vet`` / ``make lint``
     - ``go vet`` / staticcheck (pinned version, run via ``go run``)
   * - ``make build``
     - Service binaries into ``bin/``
   * - ``make bundle`` / ``make run``
     - Binaries with the web UI embedded in Core / build and run the whole
       system (:doc:`/how-to/run-locally`)
   * - ``make failure-test``
     - Failure injection with the real binaries (below)
   * - ``make images`` / ``make dashboards``
     - Container images (:doc:`/how-to/install-single-host`) / regenerate
       the Grafana dashboards
   * - ``make tidy``
     - ``go mod tidy`` per module and ``go work sync``
   * - ``make web-install`` / ``make web-check`` / ``make web-e2e``
     - Web dependencies, checks, Playwright end-to-end tests
   * - ``make docs`` / ``make docs-serve`` / ``make docs-check``
     - Documentation build, live preview, strict build + link check
   * - ``make clean``
     - Remove build artifacts

Before committing: ``make check`` and, when docs changed,
``make docs-check``.

Continuous integration
----------------------

GitHub Actions (``.github/workflows/ci.yml``) runs on every pull request
and push to ``develop`` and ``main``, with one job per area:

.. list-table::
   :header-rows: 1

   * - Job
     - Runs
   * - Go
     - ``make go-check``
   * - Web
     - ``make web-install web-check web-build``
   * - Web e2e
     - ``make web-e2e`` (Chrome preinstalled on the runner); Playwright
       results are uploaded on failure
   * - Agent (root)
     - ``sudo -E make agent-root-test``: sessions run as another OS user
   * - Failure injection
     - ``make failure-test``
   * - Images
     - ``make images`` and ``docker compose config`` of the installation
   * - Docs
     - ``make docs-check``

A pull request is merged only when all jobs pass.

Failure injection
-----------------

``make failure-test`` (``core/test/failure``) builds the real binaries and
runs Core, Knowledge, the LLM gateway, an agent
and the fake Anthropic API as processes on free ports, with an in-process
OIDC issuer. A shell script (``fakeagent.sh``) stands in for Claude Code:
it calls the gateway, works for a few seconds, calls it again and reports
a result. Each test starts a ticket on a one-stage pipeline and kills a
service (``SIGKILL``) while the session runs:

- **Core** killed and restarted: the agent keeps the session and
  delivers its result to the new Core; the ticket finishes.
- **Agent** killed and restarted: the session is lost; the stage fails
  and the ticket waits with a question for the humans.
- **Gateway** restarted: the session's next call reaches it; the ticket
  finishes.
- **Gateway** down for good: the session fails; the ticket is flagged
  with a question.

The tests are skipped unless ``BALLET_FAILURE_TESTS=1`` (the make target
sets it); a failing test prints the tail of every service's log.
