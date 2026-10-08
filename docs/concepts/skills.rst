Skills
======

Skills are how Ballet encodes **process**. A skill is a versioned bundle
of instructions (a ``SKILL.md`` plus supporting files) — the format
Claude Code uses — that tells an agent *how* to work: branching rules,
testing approach, documentation duties, review checklists, architecture
conventions.

Ballet itself prescribes no process. Each project chooses its skills;
Ballet ships only neutral starter templates (e.g. for the default
pipeline's stage roles) that projects copy and adapt.

Skill registry
--------------

Ballet keeps a central skill registry
(:doc:`/architecture/decisions/0010-central-skill-registry`):

- **Scopes**: organization → customer → project. A lower scope may add
  skills or override a higher-scope skill of the same name. A project's
  effective set contains every *published* skill of its chain; drafts
  that were never published are not used.
- **Drafts and versions**: admins edit a skill's draft; publishing
  snapshots it as the next immutable version. Projects pin versions in
  their process profile, so a skill change never alters a running project
  unexpectedly.
- **Storage**: skills live only in the Ballet database, authored and
  versioned in the UI; they are searchable like all other content.
- **Delivery**: when a run starts, Ballet resolves the project's skill
  set and the agent writes it into the session's ``HOME`` in the layout
  the runtime expects (``.claude/skills/`` for Claude Code,
  ``.config/opencode/skills/`` for opencode).

Skills are shared across customers only at organization scope; a
customer-scoped skill is as private as that customer's knowledge.

In the UI
~~~~~~~~~

*Skills* in the header lists the skills of one scope (the organization,
or for users without organization-wide access, their first customer or
project). Users with ``skill.write`` on the scope create skills and
edit the draft: description, ``SKILL.md`` (Markdown with preview) and
supporting files. *Publish* snapshots the saved draft as the next
version; *Save and publish* does both. The page shows the unpublished
changes as a line diff against the latest version, and each version as a
diff against the one before it.

A project's *Skills* page (linked from the board) shows its effective
skills. With ``skill.write`` on the project, each skill can follow the
latest version, be pinned to a version, or be disabled for the project.

Process profile
---------------

Each project's process profile binds together:

- the pipeline definition(s) per ticket type (:doc:`pipeline`);
- the skill set and pinned versions;
- the agent pool (built from the project's devcontainer);
- branch naming, base branch and merge strategy;
- default execution policy (review mode, merge mode).
