Run Ballet on Ballet
====================

Ballet's own repository can be a Ballet project: agents implement
Ballet's tickets overnight while you sleep, and you read the digest in
the morning. This is how Ballet is dogfooded.

What you need
-------------

- A running Ballet (:doc:`run-locally` or :doc:`install-single-host`).
- An **Anthropic API key**.
- A **GitHub token** for the repository that can push branches and open
  and merge pull requests (fine-grained: *Contents* and *Pull requests*
  read/write, *Checks* and *Commit statuses* read).
- Agent sessions need Ballet's toolchains (Go, Bun, uv, git, Claude
  Code):

  - with the compose installation, run the agent from Ballet's own
    agent pool — this repository's devcontainer with the ballet-agent
    Feature (:repo:`.devcontainer/dogfood/devcontainer.json`,
    :doc:`agent-pools`):

    .. code-block:: bash

       make feature
       devcontainer build --workspace-folder . --config .devcontainer/dogfood/devcontainer.json \
         --image-name ballet-dogfood
       BALLET_AGENT_IMAGE=ballet-dogfood docker compose -f deploy/compose.yaml up -d agent

  - with ``make run`` in the devcontainer, the sessions use its tools.

Set up the project
------------------

.. code-block:: bash

   ANTHROPIC_API_KEY=… GITHUB_TOKEN=… scripts/dogfood-setup.py --import-issues

The script (:repo:`scripts/dogfood-setup.py`) is idempotent; run it again
after changing skills or settings. It

- creates customer ``ballet`` and project ``BAL``;
- stores the Anthropic key (customer) and the git token (project);
- configures execution: this repository, branches
  ``feature/{ticket}_{slug}`` from ``develop`` (GitFlow), the GitHub
  forge;
- imports ``.claude/skills`` as the project's skills (development, TDD,
  documentation, architecture, GitFlow, …), so sessions follow the same
  rules as this repository's human-guided work;
- sets budgets: 3 million tokens per ticket, 30 million per day
  (``TICKET_TOKENS``, ``DAILY_TOKENS``);
- with ``--import-issues``, creates a backlog ticket for every open
  GitHub issue labelled ``task``, with its acceptance criteria.

Other settings: ``BALLET_FORGE`` (``github`` or ``git``),
``ISSUES_TOKEN`` and ``ISSUES_REPO`` (where ``--import-issues`` reads),
``BALLET_POOL`` (``ballet`` to send the runs to the dogfooding pool;
default: any agent), ``BALLET_URL`` (default ``http://localhost:8080``),
``BALLET_TOKEN`` (a platform admin's bearer token; not needed
without authentication, else default: the development realm's
``alice``), ``BALLET_REPO``.

Rehearse first
--------------

A rehearsal runs the same setup without real credentials and without
touching GitHub: a local mirror as the repository, the plain git forge,
and the fake LLM, whose scripted tool call makes each session commit and
push.

.. code-block:: bash

   git clone --bare . .run-rehearsal/ballet-mirror.git
   BALLET_RUN_DIR=$PWD/.run-rehearsal BALLET_FAKE_LLM=1 make run &
   ANTHROPIC_API_KEY=sk-fake GITHUB_TOKEN=placeholder BALLET_FORGE=git \
     BALLET_REPO=file://$PWD/.run-rehearsal/ballet-mirror.git \
     ISSUES_TOKEN=$(gh auth token) ISSUES_REPO=denyszorinets/ballet \
     scripts/dogfood-setup.py --import-issues

Then add tickets whose description is a fake-LLM tool call, e.g. ``/tool
Bash {"command": "echo x >> docs/note.txt && git add -A && git commit -qm
note && git push -q origin HEAD"}``, and move them to *Ready*. Each runs
implement → review → verify as real Claude Code sessions, pushes
``feature/BAL-N_…`` branches to the mirror and waits for the merge (the
plain git forge leaves merging to humans); merge one into ``develop`` in
the mirror and the ticket finishes. The digest then shows the night.

Before the night
----------------

#. Choose the night's work: move tickets to **Ready** (the scheduler
   starts them, at most two at a time per project by default) — or plan
   new ones with the planner. Prefer small, independent tickets with
   clear acceptance criteria.
#. Check the ticket policy: *review: agent, merge: auto* merges into
   ``develop`` once CI passes; choose *manual* merge for tickets you want
   to merge yourself.
#. Check the pipeline (project → *Pipelines*): implement → review →
   verify → integrate by default.
#. Know the brakes: pause or *Stop all runs* on the project page
   (:ref:`concepts-unattended-pause`).

In the morning
--------------

- **Digest** (project → *Digest*): what was done, merged, failed, what
  waits, tokens used.
- **Inbox**: questions the planner could not answer.
- **Assumptions** (project → *Assumptions*): confirm or reject what
  agents assumed.
- Ticket **timelines** for the details, and the Grafana dashboards when
  installed.
