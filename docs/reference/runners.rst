Runners
=======

A **Runner** executes agent sessions — **runs** — for Core
(:doc:`/architecture/decisions/0009-devcontainer-per-run-on-docker-or-podman`).
Runners connect to Core; Core never connects to them, so Runners can sit
behind NAT on any machine with a container runtime.

Runs
----

A run is one session executing one pipeline **stage** of one ticket. Its
**spec** says what to execute: ``command``, ``env``, ``workdir``,
``timeout_seconds`` and, for container backends, ``image``.

.. mermaid::

   stateDiagram-v2
     [*] --> queued
     queued --> starting: assigned to a Runner
     queued --> cancelled
     starting --> queued: Runner refused it
     starting --> running: session started
     starting --> failed
     running --> succeeded: exit code 0
     running --> failed: non-zero exit, error, timeout, Runner gone
     running --> cancelled
     succeeded --> [*]
     failed --> [*]
     cancelled --> [*]

- Core assigns queued runs, oldest first, to the connected Runner with
  the most free capacity.
- A run fails when its session exits non-zero, cannot start, times out
  (the spec's ``timeout_seconds`` or the Runner's
  ``runner.default_timeout``), or when its Runner stays disconnected for
  more than **2 minutes**.
- Output is kept per run up to **8 MiB**; later output is dropped.
- Every change records an event (``run.queued``, ``run.started``,
  ``run.finished`` with ``status`` and ``exit_code``).

Runs are listed on their ticket and over REST
(:ref:`reference-rest-runs`). Until the orchestrator queues runs
automatically (M5), people with ``run.manage`` queue them by hand.

.. _reference-runners-workspace:

Workspace preparation
---------------------

A project's **execution settings** (:ref:`reference-rest-execution`) say
how its runs execute. When they name a repository, every run starts by
preparing its workspace, in the session itself, before the run's command:

#. git identity (``git_name`` / ``git_email``, default ``Ballet Agent`` /
   ``agent@ballet.invalid``) and, for pushing, a git credential helper
   that reads the token from ``BALLET_GIT_TOKEN``;
#. ``git clone <repo_url> repo`` and change into ``repo``;
#. check out the **ticket branch**: the existing remote branch (later
   stages continue where earlier ones pushed) or a new branch from
   ``default_branch``. The branch is named by ``branch_template`` with
   ``{ticket}`` (key), ``{slug}`` (of the title) and ``{type}`` (ticket
   type); the default is ``ballet/{ticket}-{slug}``;
#. the ``setup`` commands, in order (any failure fails the run);
#. the run's command, in the repository.

Runs also get the project's ``image`` (unless their spec sets one), its
``env``, and ``BALLET_TICKET``, ``BALLET_STAGE`` and ``BALLET_BRANCH``.
The run records its branch.

**Git token.** Set it as the project's (or customer's) ``git`` credential
(``PUT /api/v1/projects/{project}/credentials/git``). It is stored
encrypted and delivered to the Runner only with ``run.start``, in the
spec's ``secret_env``: never stored with the run, never in the remote URL,
logs or events, and never served to other services. With the Docker
backend it is part of the container's configuration while the container
exists.

.. _reference-runners-agents:

Agent runs
----------

An **agent run** executes a coding agent instead of a plain command
(:doc:`/architecture/decisions/0003-orchestrate-existing-coding-agents`).
An **adapter** turns the session — prompt, instructions, the project's
skills, MCP servers — into files, environment and a command, and reads
the agent's result back when the run finishes.

**Claude Code** (adapter ``claude-code``) runs ``claude -p`` headless
(``--output-format stream-json --permission-mode bypassPermissions``,
``IS_SANDBOX=1``) in the repository, with the prompt on standard input.
Everything it gets lives in ``$HOME`` (``<workspace>/.home``), outside the
repository, so agents cannot commit it:

- ``.claude/skills/<name>/SKILL.md`` and the skill's files — the
  project's skills in the versions it resolves (pins apply);
- ``.claude/CLAUDE.md`` — standing instructions (ticket, stage);
- ``.claude.json`` — MCP servers: the tracker (:doc:`tracker-mcp`) and
  the customer's knowledge (:doc:`knowledge-mcp`), authenticated with the
  run's token (``Bearer ${BALLET_RUN_TOKEN}``, expanded by Claude Code).

**Onboarding bundle.** The prompt is the run's onboarding bundle, built
by Core when the run is queued (Markdown, at most 60 000 bytes):

- the ticket — title, description, type, state, policy, acceptance
  criteria — the stage and what it asks for (neutral defaults for
  ``implement``, ``review``, ``verify`` and ``integrate``; projects add
  their process through skills), and the ticket branch;
- its epic and milestone;
- its dependencies, each with the summary of its latest finished agent
  run;
- knowledge: entries linked to the ticket, then up to five more found by
  searching the ticket's title in the project (read with a short-lived
  Core token limited to reading the customer's knowledge);
- the prompt given when queuing, as additional instructions.

Within the size limit the ticket and the additional instructions always
fit; the plan, dependencies and knowledge are shortened or left out, in
that order of importance, with a note saying so. The standing
instructions in ``CLAUDE.md`` tell the agent that it works unattended,
should use the knowledge tools, and must commit, push and end with a
summary.

Each run gets its own **run token** (kind ``run``, the run's customer,
project and ticket; audiences ``gateway``, ``knowledge``, ``core``;
``llm.invoke``, ``knowledge.read``/``write``, ``tracker.read``/``report``;
valid for ``agents.run_token_ttl``), issued when the run starts and
delivered as ``BALLET_RUN_TOKEN`` in ``secret_env``. Claude Code uses it as
its API key against the LLM gateway (``ANTHROPIC_BASE_URL``), so its
usage is metered to the ticket.

When the run finishes, Core reads the final ``result`` event from the
run's output: the agent's final message, turns and cost become the run's
``result``. A session that exits 0 but reports an error, or reports
nothing, fails the run.

Protocol
--------

JSON-RPC over WebSocket (the transport of :doc:`realtime-api`, same
authentication, refresh and heartbeats) at ``/runner/rpc``. The token is
Core's runner token (``data/service-tokens/runner.token``, audience
``core``, capability ``runner.connect``). Types are in
:repo:`kit/runnerproto/proto.go`.

.. list-table::
   :header-rows: 1

   * - Method
     - Direction
     - Params
   * - ``runner.hello``
     - Runner → Core
     - ``{"runner", "labels", "capacity", "active"}`` — first, once per
       connection. ``runner`` is unique among connected Runners
       (``-32009`` otherwise). Runs Core assigned to this Runner that are
       not in ``active`` fail.
   * - ``run.start``
     - Core → Runner
     - ``{"run", "spec"}``; ``spec.secret_env`` holds secrets the Runner
       adds to the session's environment and must never log or store. An
       error result (e.g. at capacity) requeues the run
   * - ``run.cancel``
     - Core → Runner
     - ``{"run"}``
   * - ``run.status``
     - Runner → Core
     - ``{"run", "status": "running"}``
   * - ``run.log``
     - Runner → Core
     - ``{"run", "stream": "stdout"|"stderr"|"system", "text"}``
   * - ``run.finished``
     - Runner → Core
     - ``{"run", "exit_code", "error"?, "cancelled"?}``

Every Runner → Core message is a request the Runner awaits, so a run's
output always reaches Core before its ``run.finished``. The Runner
batches output per stream (every 200 ms, at a stream change, or at
16 KiB).

A Runner reconnects with exponential backoff (0.5 s doubling to 30 s,
with jitter), introduces itself with the runs it still executes or
holds an undelivered result for, and then delivers those results. Core
fails the runs a reconnecting Runner does not claim. Output produced
while disconnected is lost.

Backends
--------

``docker`` (default)
   Each run gets a **fresh container** from the spec's ``image``, through
   the Docker Engine API (Podman's compatible API works too; API
   ``v1.41``). The Runner pulls the image if needed, creates the container
   (named ``ballet-run-<run>``, label ``ballet.run``, ``--init``, CPU and
   memory limits), streams its stdout and stderr, waits for it, and
   removes it. The workspace is ``/workspace`` in the container; the
   spec's ``workdir`` is relative to it. Cancelling stops the container
   (``SIGTERM``, killed after 10 s).

``process`` — **development only**
   Each run executes as a local process in a fresh temporary workspace,
   removed afterwards (:doc:`/architecture/decisions/0023-process-backend-for-development`).
   The environment is minimal — ``PATH``, ``LANG``, ``TZ``, ``HOME``
   inside the workspace, ``BALLET_WORKSPACE`` and the spec's ``env``;
   ``image`` is ignored. Cancelling stops the whole process group. There
   is **no isolation**: the session can do anything the Runner's user can.
   The Runner logs a warning when it starts with this backend.

Run the Docker backend's integration tests against a local engine with
``make runner-docker-test`` (CI runs them on every pull request).

Configuration
-------------

``BALLET_RUNNER_*`` environment variables override the TOML file
(``-config``), as for every service (:doc:`configuration`).

.. list-table::
   :header-rows: 1

   * - Key
     - Default
     - Meaning
   * - ``core.url``
     - ``http://localhost:8080``
     - Core's base URL (``http`` → ``ws``)
   * - ``core.token_file``
     - ``data/service-tokens/runner.token``
     - The runner token Core writes; read on every connection
   * - ``runner.name``
     - host name
     - Unique name of this Runner
   * - ``runner.capacity``
     - ``2``
     - Concurrent runs
   * - ``runner.labels``
     - ``[]``
     - ``key=value`` labels; ``backend=<runner.backend>`` is added
   * - ``runner.backend``
     - ``docker``
     - Execution backend
   * - ``runner.default_timeout``
     - ``2h``
     - Timeout of runs whose spec sets none (at least ``1m``)
   * - ``docker.host``
     - ``DOCKER_HOST`` or ``unix:///var/run/docker.sock``
     - Engine endpoint: ``unix://``, ``tcp://`` or ``http(s)://``; checked
       at start-up
   * - ``docker.cpus`` / ``docker.memory_mb``
     - ``0`` (unlimited)
     - Limits per container
   * - ``docker.network``
     - engine default
     - Network of the containers
   * - ``process.work_dir``
     - OS temp dir
     - Parent of the run workspaces (process backend)
   * - ``process.keep_workspaces``
     - ``false``
     - Keep workspaces after runs, for debugging

Run a development Runner next to Core (no container runtime needed):

.. code-block:: bash

   BALLET_RUNNER_RUNNER_BACKEND=process BALLET_RUNNER_RUNNER_NAME=dev-1 \
     go run ./runner/cmd/runner
