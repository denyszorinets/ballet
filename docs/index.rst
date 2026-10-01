Ballet
======

Ballet is a work-orchestration platform for AI software development. A
human engineer plans work together with a **planner agent**; the plan
becomes tickets; tickets are executed by autonomous **coding agents**
(Claude Code, Codex, opencode, …) each in a fresh devcontainer. Ballet
does not build coding agents — it choreographs them: it decides what runs
when, gives every run complete context, enforces process, and keeps
humans in control.

.. note::

   Ballet is in the design phase. These pages describe the intended
   system; decisions marked *Proposed* are open for discussion.

.. toctree::
   :maxdepth: 2
   :caption: Concepts

   concepts/index

.. toctree::
   :maxdepth: 2
   :caption: Architecture

   architecture/index

.. toctree::
   :maxdepth: 2
   :caption: Development

   development/index
