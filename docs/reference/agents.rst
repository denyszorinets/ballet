Agents
======

An **agent** executes coding-agent sessions — **runs** — for Core
(:doc:`/architecture/decisions/0025-agent-fleet-runs-sessions-as-processes`).
It is a long-lived process, one per container or VM, that connects to
Core and runs each session as an OS process in its own environment.
Agents connect to Core; Core never connects to them, so agents can sit
behind NAT. Ballet does not create containers: a fleet is N agent
containers (a Kubernetes Deployment, compose, systemd on VMs).

Runs
----

A run is one session executing one pipeline **stage** of one ticket. Its
**spec** says what to execute: ``command``, ``env``, ``workdir``,
``timeout_seconds``. ``image`` is recorded but not used by agents.

.. mermaid::

   stateDiagram-v2
     [*] --> queued
     queued --> starting: assigned to an agent
     queued --> cancelled
     starting --> queued: agent refused it
     starting --> running: session started
     starting --> failed
     running --> succeeded: exit code 0
     running --> failed: non-zero exit, error, timeout, agent gone
     running --> cancelled
     succeeded --> [*]
     failed --> [*]
     cancelled --> [*]

- Core assigns queued runs, oldest first, to the connected agent with
  the most free capacity.
- A run fails when its session exits non-zero, cannot start, times out
  (the spec's ``timeout_seconds`` or the agent's
  ``session.default_timeout``), or when its agent stays disconnected for
  more than **2 minutes**.
- Output is kept per run up to **8 MiB**; later output is dropped.
- Every change records an event (``run.queued``, ``run.started``,
  ``run.finished`` with ``status`` and ``exit_code``).

Runs are listed on their ticket and over REST
(:ref:`reference-rest-runs`). Until the orchestrator queues runs
automatically (M5), people with ``run.manage`` queue them by hand.

.. _reference-agents-workspace:

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

Runs also get the project's ``env``, and ``BALLET_TICKET``, ``BALLET_STAGE`` and ``BALLET_BRANCH``.
The run records its branch.

**Git token.** Set it as the project's (or customer's) ``git`` credential
(``PUT /api/v1/projects/{project}/credentials/git``). It is stored
encrypted and delivered to the agent only with ``run.start``, in the
spec's ``secret_env``: never stored with the run, never in the remote URL,
logs or events, and never served to other services. It exists only in
the session's environment.

.. _reference-agents-runs:

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
Core's agent token (``data/service-tokens/agent.token``, audience
``core``, capability ``runner.connect``). Types are in
:repo:`kit/runnerproto/proto.go`; method names keep the earlier
``runner`` prefix.

.. list-table::
   :header-rows: 1

   * - Method
     - Direction
     - Params
   * - ``runner.hello``
     - agent → Core
     - ``{"runner", "labels", "capacity", "active"}`` — first, once per
       connection. ``runner`` is the agent's name, unique among connected agents
       (``-32009`` otherwise). Runs Core assigned to this agent that are
       not in ``active`` fail.
   * - ``run.start``
     - Core → agent
     - ``{"run", "spec"}``; ``spec.secret_env`` holds secrets the agent
       adds to the session's environment and must never log or store. An
       error result (e.g. at capacity) requeues the run
   * - ``run.cancel``
     - Core → agent
     - ``{"run"}``
   * - ``run.status``
     - agent → Core
     - ``{"run", "status": "running"}``
   * - ``run.log``
     - agent → Core
     - ``{"run", "stream": "stdout"|"stderr"|"system", "text"}``
   * - ``run.finished``
     - agent → Core
     - ``{"run", "exit_code", "error"?, "cancelled"?}``

Every agent → Core message is a request the agent awaits, so a run's
output always reaches Core before its ``run.finished``. The agent
batches output per stream (every 200 ms, at a stream change, or at
16 KiB).

An agent reconnects with exponential backoff (0.5 s doubling to 30 s,
with jitter), introduces itself with the runs it still executes or
holds an undelivered result for, and then delivers those results. Core
fails the runs a reconnecting agent does not claim. Output produced
while disconnected is lost.

.. _reference-agents-sessions:

Sessions
--------

Each run executes as a process group in a **fresh workspace** (a
temporary directory under ``session.work_dir``), removed afterwards. The
spec's ``files`` are written into it first and its ``workdir`` is
relative to it.

- The environment is minimal — ``PATH``, ``LANG``, ``TZ``, ``HOME``
  (``<workspace>/.home``), ``BALLET_WORKSPACE``, the spec's ``env`` and
  ``secret_env`` — so sessions do not inherit the agent's configuration.
- With ``session.user`` set, the session runs as that OS user and owns
  its workspace. The agent must run as root to switch users; sessions
  then cannot read the agent's token file (mode ``0600``) or signal the
  agent. Without it, sessions run as the agent's own user, and the agent
  warns at start-up when that is root.
- Cancelling stops the whole process group (``SIGTERM``, ``SIGKILL``
  after 10 s).

The container or VM is the isolation boundary: sessions share it with the
agent and with later sessions, separated by workspaces and the session
user. Run agents of different customers in different containers.

The agent image (``make images`` builds ``ballet-agent`` from
:repo:`deploy/Containerfile`) has git, Claude Code and common build
tools, a session user ``ballet`` and ``BALLET_AGENT_SESSION_USER=ballet``.
Projects needing more toolchains extend it, as
:repo:`deploy/agent/ballet.Containerfile` does for Ballet itself.

Test switching users (needs root; CI runs it on every pull request) with
``sudo -E make agent-root-test``.

Configuration
-------------

``BALLET_AGENT_*`` environment variables override the TOML file
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
     - ``data/service-tokens/agent.token``
     - The agent token Core writes; read on every connection
   * - ``agent.name``
     - host name plus a random suffix
     - Unique name of this agent. Set a stable name to let a restarted
       agent tell Core at once which runs it lost
   * - ``agent.capacity``
     - ``1``
     - Concurrent sessions
   * - ``agent.labels``
     - ``[]``
     - ``key=value`` labels
   * - ``session.user``
     - none: the agent's user
     - OS user sessions run as (needs the agent to run as root)
   * - ``session.work_dir``
     - OS temp dir
     - Parent of the session workspaces
   * - ``session.keep_workspaces``
     - ``false``
     - Keep workspaces after sessions, for debugging
   * - ``session.default_timeout``
     - ``2h``
     - Timeout of runs whose spec sets none (at least ``1m``)

Run an agent next to Core:

.. code-block:: bash

   BALLET_AGENT_AGENT_NAME=dev-1 go run ./agent/cmd/agent
