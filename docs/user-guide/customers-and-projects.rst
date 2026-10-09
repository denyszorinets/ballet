Customers and projects
======================

Ballet is built to run an AI development shop: one platform working
for several customers, each with several projects
(:doc:`/concepts/domain-model`).

Customers
---------

A **customer** is the isolation boundary. Its projects share one
knowledge base, LLM credentials and budgets; nothing — code, tickets,
knowledge, keys — crosses to another customer. Create customers on the
**Customers** page (platform admins). A customer's key, such as
``acme``, never changes; its name can.

The customer page holds:

**Projects**
   The customer's projects; create new ones under *New project*.
**Knowledge**
   The customer's knowledge base: documents, decisions, notes and debt
   records written by humans, the planner and agents
   (:doc:`/concepts/knowledge`).
**Budget**
   Tokens per ticket and per day across the customer's projects.
**LLM credentials**
   The model keys sessions and the planner use, stored encrypted.

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
- the **git token**, the project's own **LLM credentials** (overriding
  the customer's) and **budget**.

All settings and their API are in :ref:`reference-rest-execution`.
