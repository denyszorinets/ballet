Vision
======

The problem
-----------

Software projects degrade over time. Not because people cannot write
code, but because the work *around* the code erodes:

- technical debt is created but never recorded;
- documentation drifts away from behavior;
- decisions live in people's heads and leave with them;
- process is followed when convenient and skipped under pressure;
- every new developer pays a long, lossy onboarding cost.

The thesis
----------

AI coding agents are now capable implementers. Unlike humans, an agent
follows an explicit process every time it is told to — and it does not
get tired of writing documentation. What agents lack is *context* and
*coordination*.

Ballet supplies both:

**Fresh developer, perfect onboarding.**
   Every ticket is executed by new agent sessions in fresh workspaces,
   in the project's devcontainer environment.
   Nothing is carried over implicitly. Instead, the run receives an
   onboarding bundle: the ticket, its place in the plan, the relevant
   knowledge and the feature's history, plus the process it must follow
   (:doc:`skills`). If the agent cannot do the job from that bundle, the
   knowledge base is missing something — and that becomes visible.

**Knowledge is written back, not left behind.**
   A ticket is not done until its knowledge write-back is: documentation
   updated, decisions recorded, debt filed. The knowledge base grows with
   every ticket instead of decaying (:doc:`knowledge`).

**The plan drives execution.**
   Milestones, epics and tickets form a dependency graph. Ballet runs
   everything that can run in parallel and holds work at sync points
   (:doc:`scheduling`).

**Every step is a separate session.**
   Implementation, review, verification and integration are performed by
   different agent sessions that share artifacts, not transcripts. No
   agent grades its own work (:doc:`pipeline`).

**Humans steer, agents execute.**
   Humans set direction by chatting with the planner agent. After that,
   they are involved only when an agent has a question it cannot resolve
   — answered in a short sub-chat, after which work continues
   (:doc:`questions`).

The goal
--------

    *The human sleeps — Ballet works.*

A human engineer plans a body of work with the planner agent in the
evening. Overnight, Ballet executes it: tickets are implemented,
independently reviewed, verified and merged, in dependency order and in
parallel where possible. Questions that block progress wait in an inbox;
everything else continues. In the morning, the human reads a digest,
answers the open questions in a few minutes, and work resumes.

The output must be **high-quality, maintainable and observable
software** — not just code that passes tests. Maintainability and
observability are enforced by skills and checked by separate review and
verification sessions, not left to the implementer's discretion.

Every step is **observable and measurable**: what ran, for how long, at
what cost, with what outcome, and why (:doc:`/architecture/observability`).

What Ballet is not
------------------

- **Not a coding agent.** Ballet runs existing agents through adapters
  (:doc:`/architecture/decisions/0003-orchestrate-existing-coding-agents`).
- **Not a git platform.** Code, diffs and code review stay on the
  organization's git platform (GitHub, GitLab, Forgejo, Bitbucket, …); Ballet
  links tickets to pull requests and tracks their state
  (:doc:`/architecture/decisions/0007-review-on-git-platforms-via-forge-adapters`).
- **Not a general project tracker for humans.** The tracker is designed
  for agents to read and write through MCP, with a UI for humans to
  supervise.

Operating model
---------------

One Ballet **platform** hosts many **organizations** — independent
companies and teams, or a development shop and the clients it works
for — each with several projects. Organization data — especially
knowledge — is strictly isolated per organization
(:doc:`domain-model`, :doc:`/architecture/security`).

First version
-------------

The first version targets one goal: **an unattended night of work on a
real project**. In scope:

- planner chat with plan-changeset approval;
- configurable per-project pipelines, with the four-stage default
  template (:doc:`pipeline`);
- the questions inbox and per-ticket sub-chats in the Ballet UI;
- two agent runtimes (Claude Code, opencode), one forge adapter
  (GitHub), a fleet of agents in containers or VMs;
- skill registry, budgets, the digest and token/cost metrics.

Later: Gantt chart UI, further runtimes and forge adapters, the lineage
graph, roles for people outside an organization, notification channels, Kubernetes.
