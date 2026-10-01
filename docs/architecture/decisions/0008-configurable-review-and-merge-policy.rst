ADR-0008: Configurable Review and Merge Policy per Ticket
=========================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Different work warrants different oversight: a security-sensitive
change needs a human review; a documentation typo does not. Ballet must
support strict projects and highly autonomous ones, and the decision to
reduce oversight must stay with humans.

Decision
--------

Each ticket has an execution policy with:

- **review mode**: ``agent`` (the pipeline's Review stage only) or
  ``agent+human`` (additionally a human approval on the pull request);
- **merge mode**: ``auto`` (merge when all gates pass) or ``manual``.

The default for new projects is ``agent`` + ``auto``: Ballet's goal is
unattended operation, with humans involved through questions
(:doc:`/concepts/questions`). The agent Review stage always runs
(:doc:`0014-separate-session-per-pipeline-stage`).

Reviews and merges take place on the git platform; Ballet applies the
policy through forge adapters
(:doc:`0007-review-on-git-platforms-via-forge-adapters`).

Defaults come from the project's process profile. Only a human with the
``engineer`` role (or higher) in scope may change a ticket's policy. The planner may suggest a policy; the suggestion is
applied only when a human approves it.

Alternatives Considered
-----------------------

Always human review
~~~~~~~~~~~~~~~~~~~

Advantages:

- Maximum safety.

Disadvantages:

- Humans become the bottleneck; work stops whenever nobody is awake.

Project-level policy only
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simpler.

Disadvantages:

- Cannot distinguish risky from trivial tickets.

Decision Criteria
-----------------

Safety, throughput, human control.

Rationale
---------

Autonomy by default serves the core goal of unattended operation; quality
is protected by independent pipeline stages rather than by human
availability. Per-ticket policy still lets humans demand oversight where
risk warrants it.

Consequences
------------

Positive
~~~~~~~~

- Work continues without humans; strict oversight remains available
  per ticket.

Negative
~~~~~~~~

- More states and combinations to test.

Risks
~~~~~

- Auto-merge with agent review can merge flawed changes; mitigated by
  gates (tests, write-back) and audit.

Follow-up
~~~~~~~~~

- Define the reviewer-agent skill.

References
----------

- :doc:`/concepts/workflow`
