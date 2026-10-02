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

  - with the **Docker backend**, build the dogfooding image:

    .. code-block:: bash

       docker build -f deploy/agent/Containerfile -t ballet-agent deploy/agent
       docker build -f deploy/agent/ballet.Containerfile -t ballet-dogfood deploy/agent

  - with the **process backend** (``make run`` in the devcontainer), the
    sessions use the host's tools.

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
  forge, and the ``ballet-dogfood`` image;
- imports ``.claude/skills`` as the project's skills (development, TDD,
  documentation, architecture, GitFlow, …), so sessions follow the same
  rules as this repository's human-guided work;
- sets budgets: 3 million tokens per ticket, 30 million per day
  (``TICKET_TOKENS``, ``DAILY_TOKENS``);
- with ``--import-issues``, creates a backlog ticket for every open
  GitHub issue labelled ``task``, with its acceptance criteria.

Other settings: ``BALLET_URL`` (default ``http://localhost:8080``),
``BALLET_TOKEN`` (an organization admin's bearer token; default: the
development realm's ``alice``), ``BALLET_REPO``, ``BALLET_AGENT_IMAGE``.

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
