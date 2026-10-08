ADR-0014: Separate Agent Session per Pipeline Stage
===================================================

:Status: Accepted
:Date: 2026-10-01

.. note::

   Amended by :doc:`0025-agent-fleet-runs-sessions-as-processes`:
   each stage is a separate session in a fresh clone with its own
   ``HOME`` on the ticket's agent, not in a fresh container.

Context
-------

Ballet aims to deliver high-quality, maintainable and observable software
with minimal human involvement. Without human reviewers, quality depends
on agents checking agents. An agent reviewing or testing its own work in
the same session is biased by its own intentions and reasoning, and
tends to confirm rather than challenge.

Decision
--------

Each ticket runs through a pipeline of stages — by default **Implement,
Review, Verify, Integrate** — and **every stage is a separate agent
session** in a fresh container, with stage-specific skills.

- Stages exchange only **artifacts**: the branch, the pull request, and
  structured stage reports. Session transcripts and reasoning are never
  passed on.
- A failing Review, Verify or Integrate stage returns the ticket to a new
  Implement session together with its report.
- An iteration limit bounds the loop; reaching it raises a question.
- Which stages exist and how they behave is defined per project
  (:doc:`0017-pipelines-defined-per-project`); the four stages above
  are the default template.

Alternatives Considered
-----------------------

Single session per ticket with self-review
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Cheapest and fastest; no handoff overhead.

Disadvantages:

- Self-confirmation bias; quality depends on one session's diligence.

Subagents within one session
~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Some context separation at lower cost.

Disadvantages:

- Orchestrated by the biased parent; not observable or enforceable by
  Ballet; runtime-specific.

Decision Criteria
-----------------

Output quality, independence of checks, observability of each step,
cost.

Rationale
---------

Separate sessions reproduce the independence of a human team (developer,
reviewer, QA, release) and make every step individually observable and
measurable. Passing artifacts instead of transcripts forces each stage to
judge what was actually produced.

Consequences
------------

Positive
~~~~~~~~

- Independent quality checks; per-stage metrics; stages can be tuned or
  replaced independently.

Negative
~~~~~~~~

- Higher token cost and latency per ticket (each stage re-reads
  context).

Risks
~~~~~

- Overly strict reviewers cause loops; mitigated by iteration limits and
  by measuring rework rate per stage.

Follow-up
~~~~~~~~~

- Define the stage report schema.
- Write stage skills (implementer, reviewer, verifier, integrator).

References
----------

- :doc:`/concepts/pipeline`
- :doc:`0008-configurable-review-and-merge-policy`
