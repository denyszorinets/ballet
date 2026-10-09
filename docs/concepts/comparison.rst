Ballet and the alternatives
===========================

:Researched: 2026-10-08

AI coding tools are converging on one idea — agents doing whole tickets —
but they differ in what they take responsibility for. This page compares
Ballet with the three groups of tools teams most often consider, so you
can tell when Ballet is the right choice and when it is not.

The landscape
-------------

**Hosted coding agents**
   Devin, the GitHub Copilot coding agent, OpenAI Codex (cloud), Cursor's
   background agents. You assign an issue; the vendor runs one agent in its cloud sandbox and
   opens a pull request for a human to review. Strong single agents, easy
   to start; the agent, the model, the sandbox and the pricing are the
   vendor's. Copilot's agent works in a GitHub Actions environment and
   pushes to a draft pull request; Devin keeps playbooks and a knowledge
   base of repository conventions; Codex runs tasks in parallel cloud
   sandboxes.

**Parallel-session managers**
   Conductor, Claude Squad, Vibe Kanban, Nimbalyst, Emdash. Desktop, terminal or local web apps that run several CLI agents
   (Claude Code, Codex, Gemini CLI, opencode, …) side by side, each in
   its own git worktree, with a board or session list and a diff view. A
   developer supervises them interactively; they do not plan, review or
   merge on their own, and they run where the developer's machine runs.

**Open-source orchestrators**
   Composio's Agent Orchestrator, Bernstein, Baton, Microsoft Conductor.
   Frameworks that dispatch issues to agents in worktrees and follow
   pull requests and CI; some add an LLM planner that produces a task
   graph (Bernstein) or pre-merge checks. Mostly single-host, with
   tracker integration through GitHub issues.

What Ballet does differently
----------------------------

Ballet is an **orchestrator of process**, not another agent. It runs
existing agents — Claude Code and opencode today — through the steps a
good team follows, and keeps humans in control of the plan.

**Plans are approved, not improvised.**
   A planner agent turns a conversation into milestones, epics and
   tickets with acceptance criteria and dependencies, proposed as a
   changeset a human approves (:doc:`workflow`). Execution follows the
   dependency graph, in parallel where it can.

**No agent grades its own work.**
   Implement, review, verify and integrate are separate sessions in
   fresh workspaces that share artifacts, never transcripts
   (:doc:`pipeline`). Hosted agents and session managers leave review to
   the human; Ballet makes it a pipeline stage and keeps human review as
   a per-ticket option.

**Questions do not need a babysitter.**
   Agents ask only what they must; the planner answers from the
   knowledge base when it can; the rest waits in one inbox. An answer
   given within minutes goes into the running session; later, the parked
   session resumes with it. Only the affected ticket waits
   (:doc:`questions`).

**Knowledge grows with every ticket.**
   Documentation, decisions and debt are written back to a per-customer
   knowledge base, and every human answer becomes a decision record
   (:doc:`knowledge`). Each new session starts from that onboarding
   bundle instead of a blank context.

**Your agents, your models, your infrastructure.**
   Ballet is self-hosted. Sessions run on a fleet of agents built from
   each project's devcontainer — a laptop, VMs or N containers on
   Kubernetes — with no Docker socket or cluster API in Ballet
   (:doc:`/architecture/decisions/0025-agent-fleet-runs-sessions-as-processes`).
   Every model call goes through Ballet's gateway with the customer's
   own key.

**Safe to leave alone.**
   Token budgets per ticket, project and customer, enforced at the
   gateway; pause and kill switches; retries and stuck detection;
   durable state; a morning digest (:doc:`unattended-operation`).

**Built for many customers.**
   Customers are hard isolation boundaries for code, knowledge, keys and
   budgets, with OIDC sign-in and role bindings
   (:doc:`/architecture/security`) — Ballet is meant to run a
   development shop, not one developer's afternoon.

At a glance
-----------

Typical of each group as of the date above; individual products vary.

.. list-table::
   :header-rows: 1
   :stub-columns: 1

   * -
     - Ballet
     - Hosted agents
     - Session managers
     - OSS orchestrators
   * - Plan from a conversation, approved by a human
     - Yes
     - No
     - No
     - Some
   * - Dependency-ordered parallel execution
     - Yes
     - No
     - Manual
     - Some
   * - Separate review and verify sessions
     - Yes
     - No
     - No
     - Some (CI and checks)
   * - Questions routed, answered live or resumed
     - Yes
     - Chat with the agent
     - Interactive only
     - No
   * - Knowledge written back per ticket
     - Yes
     - Vendor knowledge features
     - No
     - No
   * - Choice of agent runtime
     - Claude Code, opencode
     - The vendor's
     - Many CLIs
     - Many CLIs
   * - Runs unattended on your servers
     - Yes
     - Vendor cloud
     - Developer's machine
     - Usually one host
   * - Budgets and kill switch
     - Yes
     - Plan limits
     - No
     - Some
   * - Multi-customer isolation
     - Yes
     - Teams and organizations
     - No
     - No

When to choose something else
-----------------------------

- **You want to pair with one agent in your editor** — use the agent
  directly (Claude Code, Cursor, Copilot). Ballet is for work you hand
  off.
- **You want results today without running anything** — a hosted agent
  is quicker to start; Ballet must be installed and given agents with
  your toolchain.
- **You need a runtime or forge Ballet lacks** — Ballet drives Claude
  Code and opencode and has a GitHub adapter (other git hosts work
  without pull-request automation); session managers support many more
  CLIs.
- **Ballet is young.** Its first version targets an unattended night of
  work on a real project; the Gantt view, more runtimes and forges and
  notification channels come later (:doc:`vision`).

Sources
-------

- `AI agent orchestration tools for coding <https://www.tembo.io/blog/ai-agent-orchestration-tools>`_, Tembo (2026).
- `Open-source agent orchestrators for AI coding <https://www.augmentcode.com/tools/open-source-agent-orchestrators>`_, Augment Code (2026).
- `The best AI coding agents in 2026, compared <https://daily.dev/blog/best-ai-coding-agents-comparison/>`_, daily.dev (August 2026).
- `GitHub Copilot: meet the new coding agent <https://github.blog/news-insights/product-news/github-copilot-meet-the-new-coding-agent/>`_, GitHub blog.
- `Vibe Kanban <https://www.vibekanban.com/>`_.
