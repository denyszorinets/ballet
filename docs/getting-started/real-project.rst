Your first real project
=======================

The :doc:`quickstart` used a fake model and a local repository. A real
project needs three things: a model key, a repository Ballet can push
to, and an agent with the project's toolchain.

1. Run Ballet without the fake model
------------------------------------

.. code-block:: bash

   make run

For a team or a server, install it with containers instead
(:doc:`/how-to/install-single-host`) and enable sign-in
(:doc:`/how-to/configure-oidc`).

2. Add a model key
------------------

On the customer page, under **LLM credentials**, save your **Anthropic**
API key. Keys are stored encrypted and never shown again. Agent sessions
never see the key: they call Ballet's LLM gateway with a short-lived run
token, and the gateway adds the key, meters every token and enforces
budgets (:doc:`/reference/llm-gateway`). A project can override the
customer's key in its settings.

3. Connect the repository
-------------------------

In the project's **Settings**:

**Repository URL**
   The HTTPS clone URL, e.g. ``https://github.com/acme/web.git``.
**Default branch**
   The branch tickets start from and merge into, e.g. ``main`` or
   ``develop``.
**Branch template**
   How ticket branches are named, e.g. ``feature/{ticket}_{slug}``
   (default ``ballet/{ticket}-{slug}``).
**Forge**
   *Automatic* uses the GitHub adapter for ``github.com``: Ballet opens
   pull requests, follows their review and CI state and — when the
   ticket's policy allows — merges them. Other hosts use *Plain git*:
   Ballet pushes branches and notices merges.
**Git token**
   A token that can push branches and, on GitHub, open and merge pull
   requests. A fine-grained GitHub token needs *Contents* and *Pull
   requests* (read and write), and *Checks* and *Commit statuses* (read).

4. Give the agents the project's toolchain
------------------------------------------

Agents verify their work by building and testing it, so sessions need
the project's compilers, package managers and services.

- With ``make run``, sessions run on your machine: whatever is installed
  there is what they have. Use **Setup commands** in the settings for
  per-workspace preparation (``npm ci``, ``go mod download``, …).
- For anything beyond a laptop, build an **agent pool** from the
  project's devcontainer: the ``ballet-agent`` devcontainer Feature adds
  the agent to the image, and every container of it serves the
  project's runs (:doc:`/how-to/agent-pools`). Set the pool's name as
  **Agent pool** in the settings.

5. Set limits
-------------

Before leaving agents alone, set **budgets** (customer page and project
settings): tokens per ticket and per day. Work over budget waits instead
of spending (:ref:`concepts-unattended-budgets`). Each ticket's **policy**
says whether a human must also approve the pull request and whether
Ballet merges or a human does; the default is fully autonomous
(*Review agent · Merge auto*).

6. Plan and run
---------------

Open **Planner** and describe what you want — a feature, a fix, a
milestone. The planner reads the project's tickets and knowledge, asks
what it needs, and proposes a changeset of epics, tickets with
acceptance criteria, and dependencies. Approve it, move the tickets you
want done to **Ready**, and follow them on the board.

Tickets should be small and independently testable; the planner is
instructed to cut them that way, and you can edit any ticket before it
runs. Continue with :doc:`/user-guide/planning`.
