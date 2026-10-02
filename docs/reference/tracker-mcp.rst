Tracker MCP
===========

Agent runs reach the tracker through an MCP server in Core (streamable
HTTP, stateless) at ``/mcp/tracker``. Every agent session gets it as the
``tracker`` server, next to ``knowledge`` (:doc:`knowledge-mcp`), set up
by its adapter (:ref:`reference-runners-agents`).

Authentication
--------------

``Authorization: Bearer <run token>`` — the run's own token (kind
``run``, audience ``core``). Every tool works on **the token's ticket
only**, and only while the run is active (starting or running); a token
for another ticket, customer or project, an unknown or finished run, and
any non-run token are refused. ``ticket_context`` needs
``tracker.read``; the other tools need ``tracker.report``.

Tools
-----

.. list-table::
   :header-rows: 1

   * - Tool
     - Input
     - Does
   * - ``ticket_context``
     - —
     - The ticket's onboarding bundle, current (ticket, plan, dependencies,
       knowledge), plus the reports and questions so far
   * - ``report_progress``
     - ``message``
     - Records a progress note
   * - ``submit_stage_report``
     - ``outcome`` (``done``, ``blocked``, ``failed``), ``summary``,
       ``details``?
     - The run's account of its stage, submitted once at the end; the
       orchestrator reads the outcome
   * - ``record_assumption``
     - ``assumption``, ``rationale``?
     - Records a reversible decision taken without asking, for later
       confirmation
   * - ``raise_question``
     - ``question``, ``context``?, ``blocking``?
     - Records a question; the planner tries to answer it from the
       knowledge base, else the humans do (:doc:`/concepts/questions`).
       Returns ``{"id", "next"}``: after a **blocking** question the
       agent pushes its work, submits its stage report and ends the
       session; a new session of the stage continues with the answer
   * - ``propose_work``
     - ``title``, ``description``, ``type``?, ``reason``
     - Proposes a new ticket, related to the run's ticket, as a **plan
       changeset** proposed by ``run:<id>`` — the humans decide; runs
       never create tickets (:ref:`reference-rest-changesets`)

Texts are Markdown, at most 20 000 characters. Every report and question
records an event on the ticket (``item.report_added``,
``item.question_raised``), so the item page updates live; humans read
them over REST (``GET /api/v1/items/{item}/reports`` and
``/questions``, :ref:`reference-rest-runs`).
