ADR-0007: Code Review on Git Platforms via Forge Adapters
=========================================================

:Status: Accepted
:Date: 2026-10-01

.. note::

   Since :doc:`0027-tenancy-platform-organization-project`, customers are
   called **organizations** and the installation-wide level formerly
   called the organization is the **platform**; this record keeps the
   terms of its time.

Context
-------

Customer code lives on various git platforms ("forges"): GitHub,
GitLab, Bitbucket, Gitea/Forgejo (including Codeberg) and others.
Issues live in Ballet, but developers and customers already review code
on their forge, and forges already provide diffs, review comments, CI
and branch protection. Ballet must still know the state of every change
to evaluate gates, apply per-ticket review and merge policy
(:doc:`0008-configurable-review-and-merge-policy`), and link tickets to
their code.

Decision
--------

Code review and diffs happen **on the forge**. Ballet does not show diffs
or host review discussions; it **links** to them and tracks their state.

Ballet integrates with forges through **forge adapters** in Core, one per
platform API. An adapter can:

- open a pull/merge request from the ticket's branch (named by the
  project's convention) to the project's base branch, with a
  description linking back to the Ballet ticket;
- report PR state: open, review approved / changes requested, CI status,
  merged, closed — via webhooks where available, polling otherwise;
- fetch review comments (input for rework runs);
- post a review on behalf of a reviewer agent (``agent`` review mode);
- merge the PR when the ticket's merge mode is ``auto`` and all gates
  pass.

Ballet stores a **Pull Request record** per ticket run: forge, URL,
number, branch, state, review and CI status. Tickets, runs and knowledge
entries link to it; the UI shows status and links out.

Planned adapters, in order: GitHub, Forgejo/Gitea (covers Codeberg),
GitLab, Bitbucket.

**Generic git fallback.** For a remote without a supported adapter, Ballet
pushes the branch and shows a link template (if configured). Merge is
detected by checking whether the branch head is reachable from the base
branch. ``agent`` review and ``auto`` merge are unavailable in this mode.

Alternatives Considered
-----------------------

Ballet-native review (diff UI and merges in Ballet)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Works with plain git; review policy and audit in one place.

Disadvantages:

- Rebuilds what every forge already does well; bypasses forge CI and
  branch protection; reviewers must use a second tool.

Links only, no API integration
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simplest.

Disadvantages:

- Ballet cannot know review/merge state, so gates, auto-review and
  auto-merge are impossible.

Decision Criteria
-----------------

Fit with existing developer and customer workflows, use of forge CI and
protections, implementation effort, support for automated policies.

Rationale
---------

Forges are where code review already works. A narrow adapter interface
gives Ballet the state and actions it needs for scheduling and policy
without duplicating forge functionality.

Consequences
------------

Positive
~~~~~~~~

- Reviewers use familiar tools; forge CI and branch protection apply.
- No diff/review UI to build in Ballet.

Negative
~~~~~~~~

- One adapter per forge API to build and maintain.
- Human review happens outside Ballet; Ballet sees it only through
  adapter state.

Risks
~~~~~

- Forge APIs differ in review semantics (approvals, required reviewers);
  the adapter interface must map them to a small common model.
- Webhooks require Ballet to be reachable from the forge; polling must
  work as a fallback.

Follow-up
~~~~~~~~~

- Define the forge adapter interface and the common PR state model.
- Implement the GitHub adapter first, then Forgejo/Gitea.

References
----------

- :doc:`/concepts/workflow`
- :doc:`/architecture/components`
- :doc:`0008-configurable-review-and-merge-policy`
