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
  skills or override a higher-scope skill of the same name.
- **Drafts and versions**: admins edit a skill's draft; publishing
  snapshots it as the next immutable version. Projects pin versions in
  their process profile, so a skill change never alters a running project
  unexpectedly.
- **Storage**: skills live only in the Ballet database, authored and
  versioned in the UI; they are searchable like all other content.
- **Delivery**: when a run starts, Ballet resolves the project's skill
  set and writes it into the container in the layout the agent runtime
  expects (``.claude/skills/`` for Claude Code, and equivalents for
  other runtimes).

Skills are shared across customers only at organization scope; a
customer-scoped skill is as private as that customer's knowledge.

Process profile
---------------

Each project's process profile binds together:

- the pipeline definition(s) per ticket type (:doc:`pipeline`);
- the skill set and pinned versions;
- the devcontainer template;
- branch naming, base branch and merge strategy;
- default execution policy (review mode, merge mode).
