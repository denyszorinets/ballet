Organizations and projects
==========================

One Ballet platform hosts many organizations, each with several
projects (:doc:`/concepts/domain-model`).

Organizations
-------------

An **organization** is the isolation boundary. Its projects share one
knowledge base, LLM credentials and budgets; nothing — code, tickets,
knowledge, keys — crosses to another organization. Create organizations on the
**Organizations** page (platform admins). An organization's key, such as
``acme``, never changes; its name can.

The organization page holds:

**Projects**
   The organization's projects; create new ones under *New project*.
**Knowledge**
   The organization's knowledge base: documents, decisions, notes and debt
   records written by humans, the planner and agents
   (:doc:`/concepts/knowledge`).
**Budget**
   Tokens per ticket and per day across the organization's projects.
**LLM credentials**
   The model keys sessions and the planner use, stored encrypted.
**Feature changes by agents**
   How agents may change the organization's features: apply and review
   afterwards, propose for approval, or not at all
   (:ref:`concepts-feature-map-policy`).

Projects
--------

A **project** is one product with one repository. Its key (``WEB``)
prefixes its items: ``WEB-1``, ``WEB-2``, … The project page is its
**board**; the links beside the title open the planner, changesets,
assumptions, pipelines, digest, skills and settings.

Project settings
~~~~~~~~~~~~~~~~

*Settings* says how the project's runs execute:

- the **repository**, its **default branch** and the **branch
  template** for ticket branches;
- the **agent pool** whose agents run its sessions — empty: any agent
  (:doc:`/how-to/agent-pools`);
- **setup commands** run in every fresh workspace before the session,
  and **environment** variables for them;
- the identity of agent commits;
- the **forge**: GitHub (pull requests, reviews, CI and merges) or plain
  git (branches only);
- the **answer window**: how long a session waits for answers before it
  parks (:doc:`sessions-and-questions`);
- **feature changes by agents**, overriding the organization's policy;
- the **git token**, the project's own **LLM credentials** (overriding
  the organization's) and **budget**.

All settings and their API are in :ref:`reference-rest-execution`.
