Pipelines
=========

A **pipeline** is the process a ticket goes through, defined per project
as data (:doc:`/architecture/decisions/0017-pipelines-defined-per-project`).
Every agent stage is a separate session
(:doc:`/architecture/decisions/0014-separate-session-per-pipeline-stage`);
stages hand over only artifacts: the branch, the pull request and stage
reports.

Definition
----------

.. code-block:: yaml

   max_iterations: 3
   stages:
     - id: implement
       kind: agent
       name: Implement
       instructions: Implement the ticket so that every acceptance criterion holds. …
       next: {done: review, failed: $question, blocked: $question}
     - id: review
       kind: agent
       instructions: Review the changes on this branch … Submit done when it is ready, failed …
       next: {done: verify, failed: implement, blocked: $question}
     - id: verify
       kind: agent
       next: {done: integrate, failed: implement, blocked: $question}
     - id: integrate
       kind: platform
       action: merge
       next: {done: $done, failed: implement, blocked: $question}

The first stage is where tickets start. Each stage:

``id``
   Unique, ``[a-z][a-z0-9_-]{0,31}``.
``kind``
   ``agent`` — a coding agent session (``adapter``, default
   ``claude-code``; optional ``model``; ``skills`` limits the project
   skills the session gets, empty means all; ``instructions`` tell it
   what this stage does). ``human`` — a human approves (``done``) or
   rejects (``failed``). ``platform`` — Ballet acts on the forge;
   ``action: merge`` opens the pull request if needed, waits for checks and
   the approvals the ticket's policy requires, and merges when the policy
   allows (:doc:`/architecture/decisions/0008-configurable-review-and-merge-policy`).
``timeout_minutes``
   0 for the default.
``next``
   Where each **outcome** leads: ``done``, ``failed`` or ``blocked`` to a
   stage id, ``$done`` (the ticket is done), ``$failed`` or ``$question``
   (ask the planner and humans, then resume the stage). Missing entries
   default to: ``done`` → the next stage (``$done`` after the last),
   ``failed`` and ``blocked`` → ``$question``. Agent stages take their
   outcome from the stage report the agent submits.

``max_iterations`` (1–20) bounds how often a ticket may go back to an
earlier stage; reaching it raises a question.

Validation rejects unknown kinds, adapters, actions and targets, duplicate
ids, stages that can never be reached, and pipelines that can never reach
``$done``.

Editing in the UI
-----------------

Project page → **Pipelines** edits a project's pipelines (needs
``project.update``; everyone else reads them):

- choose ``default`` or the pipeline of a ticket type (a type without its
  own pipeline starts from ``default``);
- **Stages**: the ordered stage list — ID, name, kind (agent session,
  human approval, platform merge), an agent stage's instructions, model,
  skills and timeout, and per outcome (*done*, *failed*, *blocked*) where
  it leads: the default (*done* → the next stage, else ``$done``; the
  others → ``$question``), another stage, ``$done``, ``$failed`` or
  ``$question``; plus the loop limit;
- **YAML**: the same pipeline as YAML, editable; **Export YAML** downloads
  it and **Import YAML** loads a file;
- problems (unknown targets, duplicate IDs, unreachable stages, …) are
  shown as you edit; **Publish** saves a new version, refused while there
  are problems or when someone published meanwhile;
- **History** lists the versions; *Load* puts an older version into the
  editor, to publish it again as the newest.

Versions and ticket types
-------------------------

A project has named pipelines: ``default`` and, optionally, one per
ticket type (``bug``, ``docs``, …). A ticket starts on the pipeline named
after its type, else ``default``, else Ballet's template above. Saving a
pipeline creates a new immutable version; a ticket runs on the version it
started with.

.. _reference-pipelines-running:

Running tickets
---------------

Starting a ticket's pipeline (a ``ready`` ticket only) moves the ticket to
``in_progress`` and records a *flow*: the pipeline version, the current
stage, the loop iteration and a status (``running``, ``waiting``,
``done``, ``failed`` or ``stopped``). Flows advance through durable jobs,
so a restart of Core resumes them where they were.

Agent stages
   Each agent stage is a separate run (a fresh agent session) with the
   stage's instructions, skills and model. The previous stage's report is
   handed over as an artifact in the prompt. The run's reported outcome
   (``done``, ``failed``, ``blocked``, …) picks the next stage.

Human stages
   The flow waits for ``approval``; a project member with
   ``tracker.write`` approves (outcome ``done``) or rejects (outcome
   ``failed``) with an optional comment, which becomes the stage report.

Platform ``merge`` stages
   Core opens or finds the ticket's pull request and then waits, polling
   the forge, according to the project's merge policy: for checks while
   they are pending, for a human review when the policy asks for one, and
   for a human to merge when the policy is ``manual`` or the forge cannot
   merge (plain git). A merged pull request ends the stage ``done``. A
   closed pull request, failing checks, requested changes or a branch that
   was never pushed end it ``failed``, with a report saying why, which
   the next agent stage receives.

Targets and limits
   ``$done`` finishes the ticket (``done``). ``$failed`` fails the flow and
   pauses the ticket. Going back to an earlier stage starts a new
   iteration; when ``max_iterations`` is exceeded, or a stage targets
   ``$question``, Core asks a blocking question on the ticket and the flow
   waits for an ``answer`` (the ticket is ``waiting_for_answer``).

Questions
   When a stage's run raised a blocking question, the stage ends without
   an outcome and the flow waits for the answer. Once no blocking
   question of the ticket is open, the flow resumes (``flow.resumed``):
   the same stage runs again in a new session, with the answered
   questions handed over (:doc:`/concepts/questions`).

Stopping
   Pausing or cancelling the ticket stops the flow and cancels its active
   run (when the stage's next step runs, at the latest at the reconciler's
   next pass).

Budgets
   An agent stage starts only within budget: a used-up ticket budget
   raises a blocking question, a used-up daily budget makes the flow wait
   (``waiting: budget``) until the reconciler finds budget again
   (:ref:`concepts-unattended-budgets`). A stage whose run failed while
   over budget waits too, instead of failing.

Project and organization pauses
   While the project or organization is paused, a flow waits before
   entering its next stage or merging (``waiting: pause``); a stage
   cancelled by the kill switch runs again after the resume
   (:ref:`concepts-unattended-pause`).

.. _reference-pipelines-scheduling:

Scheduling
~~~~~~~~~~

Core's scheduler starts pipelines without human action. A ticket is
*runnable* when it is ``ready``, its project has a repository configured,
and every item that blocks it — or blocks its epic — is ``done`` or
``cancelled``. Runnable tickets start oldest first, as long as the
:ref:`concurrency limits <reference-config-scheduler>` allow: a flow
occupies a slot while it runs a stage or waits for checks, not while it
waits for a human (an approval, a review, a merge or an answer). So
resolving a blocker, moving a ticket to ``ready`` or finishing a flow
starts the next tickets within seconds.

.. _reference-pipelines-recovery:

Recovery and stuck stages
~~~~~~~~~~~~~~~~~~~~~~~~~

Flows survive restarts of Core and Runners: their steps are durable jobs,
and a Runner that stays away longer than its grace period fails its runs,
which ends the stage ``failed``. In addition, Core's reconciler checks
every active flow periodically (``[reconciler] interval``, default one
minute):

- a flow whose ticket is no longer ``in_progress`` or
  ``waiting_for_answer`` (a human paused or cancelled it) is stopped, and
  its run cancelled;
- a stage run that made no progress beyond its timeout (the stage's
  ``timeout_minutes``, else two hours) plus ``[reconciler] slack`` is
  cancelled, and the flow is *flagged*;
- the step a flow waits for — entering a stage, handling a finished run,
  checking a pull request — is queued again when it was lost; when it
  failed for good the flow is flagged;
- stage runs that no flow waits for any more are cancelled.

Flagging records ``flow.stuck`` with the reason, raises a blocking
question on the ticket and makes the flow wait for an ``answer`` (the
ticket is ``waiting_for_answer``).

Every transition is recorded in the ticket history (``flow.started``,
``flow.stage_started``, ``flow.stage_finished``, ``flow.waiting``, ``flow.resumed``, ``flow.stuck``, ``flow.done``,
``flow.failed``, ``flow.stopped``).

REST
----

``GET /api/v1/projects/{project}/pipelines`` → ``200``
   The latest version of each pipeline; Ballet's template appears as
   ``default`` version 0 until the project saves one. ``tracker.read``.

``GET /api/v1/projects/{project}/pipelines/{name}[?version=N]`` → ``200``
   ``{"project", "name", "version", "definition", "created_by",
   "created_at"?}``.

``GET /api/v1/projects/{project}/pipelines/{name}/versions`` → ``200`` newest first

``PUT /api/v1/projects/{project}/pipelines/{name}`` — ``{"definition", "version"}`` → ``200``
   Saves a new version after the version read (0 for the first). Needs
   ``project.update``. ``400`` for an invalid definition or name, ``409``
   when another version was saved meanwhile.

``POST /api/v1/pipelines/parse`` — ``{"yaml"}`` → ``200`` ``{"definition"?, "errors"}``
   Parses YAML (unknown fields are errors) and validates it.

``POST /api/v1/pipelines/render`` — ``{"definition"}`` → ``200`` ``{"yaml"}``

``GET /api/v1/items/{item}/flow`` → ``200``
   The ticket's flow: ``{"ticket", "pipeline", "pipeline_version",
   "stages", "stage", "iteration", "max_iterations", "status",
   "waiting"?, "run"?, "outcome"?, "report"?, "started_at",
   "updated_at", "version"}``. ``404`` before it started. ``tracker.read``.

``POST /api/v1/items/{item}/flow/start`` → ``200``
   Starts the pipeline of a ``ready`` ticket. Needs ``run.manage``;
   ``409`` when the ticket is not ready or its flow is still active.

``POST /api/v1/items/{item}/flow/approve`` and ``.../flow/reject`` — ``{"comment"?}`` → ``200``
   Decides the human stage the flow waits on. Needs ``tracker.write``;
   ``409`` when the flow does not wait for an approval.
