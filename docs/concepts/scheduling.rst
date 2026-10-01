Scheduling
==========

The dependency graph drives execution
-------------------------------------

Milestones, epics and tickets are nodes; ``blocks`` dependencies are
edges, at any level (a ticket can block a milestone, a milestone can block
a ticket). A ticket is **runnable** when it is in state ``ready`` and
every item that blocks it is resolved — ``done``, or ``cancelled`` so that
abandoned work never stalls its dependents.

Ballet rejects any ``blocks`` edge that would create a cycle. Each
dependency change also bumps the project's graph version, so two
concurrent additions cannot jointly create a cycle: the second one is
re-validated against the updated graph.

Dependencies are limited to items of the same project for now.

The scheduler keeps a ready queue and starts runs up to the configured
concurrency limits (globally, per customer, per project). Anything ready
runs in parallel; nothing starts before its prerequisites.

The Gantt chart is a forecast
-----------------------------

The Gantt chart is the primary human view of a project, computed from the
same graph:

- **lanes** show which work can proceed in parallel;
- **sync points** are milestones and other join nodes where parallel
  work must converge;
- the **critical path** shows which tickets determine the end date;
- durations come from estimates first, then from historical run
  durations of similar tickets.

Changing the plan (adding a dependency, re-ordering, splitting a ticket)
changes the forecast immediately. The Gantt chart never triggers work by
itself — the graph and the ready queue do. This keeps execution correct
even when estimates are wrong.

Sync points
-----------

At a milestone, Ballet can require a human gate before dependent work
starts — a natural moment for the human and planner to review direction,
update documentation and re-plan.
