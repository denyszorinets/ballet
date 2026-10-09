Pipelines and skills
====================

Ballet prescribes no development process. A project's process is its
**pipeline** — which stages run and in what order — and its **skills** —
how agents work in each stage.

Pipelines
---------

*Pipelines* on the project page edits the project's pipeline. Each
stage has an **ID**, a **name**, a kind and, for agent stages, the
**skills** it uses (empty: all project skills), the **model** and a
**timeout**:

**agent session**
   A coding-agent session, e.g. Implement, Review, Verify.
**human approval**
   Waits for a human to approve or reject on the ticket page.
**platform (merge)**
   Merges the pull request through the forge when its gates pass.

Problems are shown as you edit; **Publish** saves the pipeline as its
next **version** — a ticket keeps the version it started with. *History*
lists the versions, and *Load* brings an older one back into the editor. A project can also have one pipeline per ticket type (``bug``, ``docs``,
…); tickets use the one named after their type, else ``default``.
**Export YAML** and the *YAML* tab move pipelines
between projects. The format is in :doc:`/reference/pipelines`; the
ideas in :doc:`/concepts/pipeline`.

Skills
------

A skill is a ``SKILL.md`` with instructions — branching rules, test
approach, documentation duties, review checklists — in the format
Claude Code uses. Ballet writes the project's skills into every session
(:doc:`/concepts/skills`).

- **Skills** in the header manages the skills of the platform or an
  organization: edit a draft, then **publish** it as the next immutable
  version. An organization's skills are private to it.
- A project's **Skills** page shows its effective skills (platform,
  organization and project scopes together). Each can follow the latest
  version, be **pinned** to a version, or be disabled for the project.

Because versions are pinned, changing a skill never surprises a running
project.
