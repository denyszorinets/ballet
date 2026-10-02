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

Versions and ticket types
-------------------------

A project has named pipelines: ``default`` and, optionally, one per
ticket type (``bug``, ``docs``, …). A ticket starts on the pipeline named
after its type, else ``default``, else Ballet's template above. Saving a
pipeline creates a new immutable version; a ticket runs on the version it
started with.

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
