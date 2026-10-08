ADR-0025: Agent Fleet Runs Sessions as Processes
================================================

:Status: Accepted
:Date: 2026-10-08

Context
-------

Today Core sends each run to a Runner, which creates a fresh container
per pipeline stage through the Docker Engine API
(:doc:`0009-devcontainer-per-run-on-docker-or-podman`) or, in
development, a local process
(:doc:`0023-process-backend-for-development`). Agent adapters live in
Core and turn a session into one command; the Runner returns its stdout
and exit code.

This has several costs:

- Ballet manages containers: a privileged component with socket access,
  two backends that behave differently, and a Kubernetes backend still
  to write.
- A session is one command run to completion. Nobody can talk to a
  running session, answer its question while it waits, or resume it.
- Every stage pays for a container start and dependency installation.

Requirements:

- **Reduce complexity** — the primary goal.
- Humans can interact with running sessions (see
  :doc:`0026-interactive-sessions-park-when-idle`).
- Run on a laptop, on VMs and on Kubernetes with the same code.
- Several agent runtimes: Claude Code, opencode, hermes and later
  others (:doc:`0003-orchestrate-existing-coding-agents`).
- Projects need their own toolchains.

Decision
--------

Ballet no longer creates containers. It runs a **fleet of long-lived
agents**: one Ballet ``agent`` binary per container or VM.

- **Placement is someone else's job.** A Kubernetes Deployment with N
  replicas, ``docker compose --scale agent=N``, a systemd unit on a VM,
  or the binary run by hand in development. Ballet uses no Docker,
  Podman or Kubernetes API.
- **The agent dials out to Core** over the existing WebSocket JSON-RPC
  link (:doc:`0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`),
  authenticated with an agent token, and announces its labels and
  capacity. The protocol evolves from today's Runner protocol.
- **Sessions are OS processes.** The agent runs ``claude``,
  ``opencode`` or another runtime directly in its own environment. The
  container or VM is the isolation boundary; nothing is nested.
- **Runtime drivers live in the agent.** Driving a runtime — starting
  it, writing its skills and MCP configuration, feeding it input,
  normalizing its events — moves from Core's adapters into the agent.
  Core sends a runtime-neutral session and receives normalized events.
  This amends :doc:`0003-orchestrate-existing-coding-agents`, whose
  principle stands.
- **A ticket stays on one agent while it is active.** An agent works on
  one ticket at a time. Each pipeline stage is still a separate session
  (:doc:`0014-separate-session-per-pipeline-stage`), started in a fresh
  clone of the ticket branch with its own ``HOME``, so stages share
  only artifacts. When the ticket is done or parked, the agent deletes
  its workspace and takes the next ticket.
- **Two OS users.** The agent runs as one user, sessions as another,
  unprivileged user. Sessions cannot read the agent's token or change
  the agent. Tokens a session needs (git, gateway, MCP) are scoped to
  the ticket and passed only into the session's environment.
- **Toolchains come from the project's devcontainer.** Ballet publishes
  a devcontainer Feature that installs the agent, the supported
  runtimes and the two users. A project adds the Feature to its
  ``devcontainer.json`` and builds the image as it already does. Each
  such image deployed N times is a **pool**; the agent reports the
  pool label, and Core routes a project's tickets to agents with the
  project's label. Ballet builds no images.
- The Runner service, its Docker and process backends, and Core's agent
  adapters are removed once the agent replaces them.

This supersedes :doc:`0009-devcontainer-per-run-on-docker-or-podman`
and :doc:`0023-process-backend-for-development`.

Alternatives Considered
-----------------------

Keep the Runner and add a Kubernetes backend
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No new component; a fresh container per stage.

Disadvantages:

- Ballet keeps managing containers on every platform, with privileged
  access to each.
- Sessions stay non-interactive; interaction would need attaching to
  container stdio through each platform's API.

Agent inside a container that the Runner creates per ticket
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Interactive sessions and a fresh container per ticket.

Disadvantages:

- Two components to build and run: a privileged provisioner and the
  agent.

Adopt an existing sandbox platform (E2B, Daytona, OpenHands runtime)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Provisioning, isolation and exec are provided.

Disadvantages:

- A large external dependency for a small surface; less control over
  the session model.

Decision Criteria
-----------------

Complexity of Ballet, interaction with running sessions, one execution
model on every platform, security of credentials, toolchain fit.

Rationale
---------

Container management is the most platform-specific and privileged part
of Ballet, and every platform already has a tool that does it well.
Leaving placement to those tools and keeping one small agent that dials
out gives the same code path on a laptop, a VM and Kubernetes, removes
the Runner and its backends, and makes sessions interactive because the
process driving the runtime sits next to it. Devcontainer Features let
projects keep the environment developers already use.

Consequences
------------

Positive
~~~~~~~~

- No container API, socket access or Kubernetes integration in Ballet.
- One execution model everywhere; development uses the real path.
- No container start per stage; toolchains are pre-installed.
- Scaling is changing N.

Negative
~~~~~~~~

- Tickets no longer get a fresh container: a later ticket runs where an
  earlier one ran, separated only by workspace deletion and OS users.
- A project must add the Feature and build and deploy its pool.
- Idle agents hold resources; a fixed pool limits concurrency.
- The agent binary and Core are released separately; the protocol needs
  a version handshake.

Risks
~~~~~

- A session that escalates privileges inside the container can reach
  the agent and later tickets in the same pool. Keep sessions
  unprivileged and pools per customer; recycle containers if this
  proves insufficient.
- Runtimes change their streaming interfaces; drivers must be kept up
  to date.

Follow-up
~~~~~~~~~

- Mark :doc:`0009-devcontainer-per-run-on-docker-or-podman` and
  :doc:`0023-process-backend-for-development` superseded (done).
- Update the architecture overview, components and security pages as
  the implementation lands.
- Optional later: recycle the container after each ticket; scale pools
  on queue depth.

Validation
----------

Dogfooding: Ballet's own devcontainer with the Feature serves as the
pool for Ballet's tickets.

References
----------

- :doc:`0003-orchestrate-existing-coding-agents`
- :doc:`0014-separate-session-per-pipeline-stage`
- :doc:`0026-interactive-sessions-park-when-idle`
- :doc:`/architecture/components`
- :issue:`160`
