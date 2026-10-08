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

An **agent run** executes a coding-agent session instead of a plain
command (:doc:`/architecture/decisions/0003-orchestrate-existing-coding-agents`).
Core sends the session independent of the runtime — runtime, prompt,
standing instructions, the project's skills, MCP servers, model, the
gateway URL and the variable holding the run token — in the spec's
``session``. The spec's ``command`` only prepares the workspace (clone,
branch, setup; :ref:`reference-agents-workspace`); after it succeeds the
agent's **driver** for the runtime runs the session in the repository
(:doc:`/architecture/decisions/0025-agent-fleet-runs-sessions-as-processes`).

The driver keeps the session open between turns: it sends the prompt as
the first message, normalizes what the session prints into **events**
and ends the session when a turn ends with nothing more to say. Events
are part of the run's output, stream ``event``, one JSON object per line:

.. list-table::
   :header-rows: 1

   * - ``kind``
     - Fields
     - Meaning
   * - ``text``
     - ``text``
     - What the agent says
   * - ``tool_use``
     - ``tool``, ``input`` (JSON)
     - The agent calls a tool
   * - ``tool_result``
     - ``text`` (at most 4 000 bytes), ``error``
     - What the tool returned
   * - ``result``
     - ``text``, ``error``
     - A turn ended, with the agent's final message

The ticket page shows them as the session's transcript, live while it
runs. The driver also reports the session's result — final message,
turns, cost, success — with ``run.finished``.

.. _reference-agents-talk:

Talking to a running session
~~~~~~~~~~~~~~~~~~~~~~~~~~~~

(:doc:`/architecture/decisions/0026-interactive-sessions-park-when-idle`)
People with ``run.manage`` send input to a running session from its
transcript (or ``POST /api/v1/runs/{run}/input``, :ref:`reference-rest-runs`):

- a **message** waits for the end of the current turn, then starts the
  next turn; it shows in the transcript as an event of kind ``user`` when
  it reaches the session;
- an **interrupt** stops the current turn at once (the running tool is
  rejected and the turn ends with a failed ``result``), then delivers its
  text, if any, as the next message. An interrupt without text ends the
  session after the stopped turn, and the run fails.

A session ends when a turn ends with no message waiting; input after
that is refused (``409``). The run's result is its last turn's, with the
turns of all turns. Additional event kind:

``user``
   ``text``: a human's message (or an answer) reached the session.

.. _reference-agents-park:

Waiting for answers, parking, resuming
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

(:doc:`/concepts/questions`) When a session raises a blocking question
through the tracker MCP, Core tells its agent to **hold** it: a turn
that ends with nothing to deliver no longer ends the session, which
waits; messages then reach it at once. An answer goes to the waiting
session as a message; once none of its blocking questions is open, Core
**releases** it and it ends with its next turn.

Core checks every 30 seconds for sessions that waited longer than their
project's ``answer_window_minutes`` (execution settings; 0: Core's
``agents.answer_window``, default ``15m``) and **parks** them: the agent
commits all changes in the repository as ``WIP: parked while waiting for
answers`` and pushes, reads the session's state from the driver (Claude
Code: its transcript, ``~/.claude/projects/*/<session>.jsonl``),
compresses it (at most 3 MiB; larger states are not kept) and reports
a result with ``parked``, ``session_id`` and ``state``. Core keeps the
state with the run; the run succeeds with ``result.parked``.

The next session of the same stage **resumes** the parked one when its
state was saved: its spec's session carries ``resume`` (the session ID;
the state is added when the run starts, never stored with it) and its
prompt is the answers. Claude Code restores the transcript and runs with
``--resume <session>``. Without a saved state the stage starts a new
session with the questions and answers in its onboarding bundle.

Claude Code
~~~~~~~~~~~

**Claude Code** (runtime ``claude-code``) runs ``claude -p --input-format
stream-json --output-format stream-json --permission-mode
bypassPermissions`` (``IS_SANDBOX=1``) in the repository; the executable
is the agent's ``drivers.claude_command``. Everything it gets lives in
``$HOME`` (``<workspace>/.home``), outside the repository, so agents
cannot commit it:

- ``.claude/skills/<name>/SKILL.md`` and the skill's files — the
  project's skills in the versions it resolves (pins apply);
- ``.claude/CLAUDE.md`` — standing instructions (ticket, stage);
- ``.claude.json`` — MCP servers: the tracker (:doc:`tracker-mcp`) and
  the customer's knowledge (:doc:`knowledge-mcp`), authenticated with the
  run's token (``Bearer ${BALLET_RUN_TOKEN}``, expanded by Claude Code).

opencode
~~~~~~~~

**opencode** (runtime ``opencode``) runs ``opencode acp`` — the Agent
Client Protocol over standard input and output — in the repository; the
executable is the agent's ``drivers.opencode_command``. The driver
initializes the connection, opens one ACP session and sends one
``session/prompt`` per turn; ``session/update`` notifications become
events (message chunks are joined into one ``text`` event, a tool call is
shown once its input is known), an interrupt is ``session/cancel``, and
permission requests are allowed. Its global configuration in ``$HOME``:

- ``.config/opencode/opencode.json`` — the Anthropic provider pointed at
  the LLM gateway (``<gateway>/v1``, the run token as API key via
  ``{env:BALLET_RUN_TOKEN}``), the model (``anthropic/<model>``, default
  ``claude-sonnet-5-5``, declared so opencode accepts it), the MCP
  servers (``remote``, ``Authorization: Bearer {env:BALLET_RUN_TOKEN}``),
  ``"permission": "allow"``, no autoupdate or sharing;
- ``.config/opencode/AGENTS.md`` — standing instructions;
- ``.config/opencode/skills/<name>/SKILL.md`` and the skill's files.

Parked opencode sessions are not resumed: the stage starts a new session
with the questions and answers in its prompt.

Prompt and run token
~~~~~~~~~~~~~~~~~~~~

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

When the run finishes, the session's result reported by the agent —
final message, turns and cost — becomes the run's
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
       adds to the session's environment and must never log or store;
       ``spec.session`` is a coding-agent session for the driver. An
       error result (e.g. at capacity) requeues the run
   * - ``run.cancel``
     - Core → agent
     - ``{"run"}``
   * - ``run.input``
     - Core → agent
     - ``{"run", "kind", "text"?}`` — input to the run's session: a
       human's ``message`` or ``interrupt``; Core's ``hold``,
       ``release`` and ``park`` (:ref:`reference-agents-park`). An error
       result when the session is not running there (any more)
   * - ``run.status``
     - agent → Core
     - ``{"run", "status": "running"}``
   * - ``run.log``
     - agent → Core
     - ``{"run", "stream": "stdout"|"stderr"|"system"|"event", "text"}``
   * - ``run.finished``
     - agent → Core
     - ``{"run", "exit_code", "error"?, "cancelled"?, "result"?}``;
       ``result`` (``{"success", "summary", "turns", "cost_usd",
       "parked"?, "session_id"?, "state"?}``) is a session's

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
:repo:`deploy/Containerfile`) has git, Claude Code, opencode and common
build tools, a session user ``ballet`` and ``BALLET_AGENT_SESSION_USER=ballet``.
Projects build agent pools with their toolchains from their
devcontainers (:doc:`/how-to/agent-pools`).

**Pools.** An agent's ``pool`` label (``agent.labels``, e.g.
``pool=web``; the devcontainer Feature sets it) puts it in a pool. Core
sends a run whose project names a pool (execution setting ``pool``) only
to agents of that pool — the least loaded with free capacity — and runs
without a pool to any agent. A run waits in the queue while no agent of
its pool has room.

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
   * - ``drivers.claude_command``
     - ``claude``
     - The Claude Code executable
   * - ``drivers.opencode_command``
     - ``opencode``
     - The opencode executable

Run an agent next to Core:

.. code-block:: bash

   BALLET_AGENT_AGENT_NAME=dev-1 go run ./agent/cmd/agent
