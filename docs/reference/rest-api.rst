REST API
========

Core's REST API serves stateless operations
(:doc:`/architecture/decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`).
Base path: ``/api/v1``.

The machine-readable contract is the OpenAPI 3 document
:repo:`core/api/openapi.yaml`, also served by Core (without
authentication) at ``GET /api/openapi.yaml``. This page explains the
API; the OpenAPI document is authoritative for exact schemas.

Conventions
-----------

Authentication
   Every ``/api/`` request needs ``Authorization: Bearer <OIDC access
   token>`` (:doc:`/how-to/configure-oidc`); otherwise ``401``.

Authorization
   Every operation is authorized for the caller at organization,
   customer or project scope; denied operations return ``403``. List
   endpoints return only the items the caller may read.

Bodies
   JSON. Unknown fields are rejected (``400``). Maximum 1 MiB.

Keys
   Customers and projects are addressed by their immutable keys
   (``acme``, ``ACME``), not by internal IDs.

Optimistic concurrency
   Every entity has a ``version``. Updates must send the ``version`` the
   client last read; if the entity changed since, the update is rejected
   with ``409 conflict`` — re-read and retry. Successful updates return
   the new version.

Lists
   ``{"items": [...]}``.

Errors
~~~~~~

.. code-block:: json

   {"error": "conflict", "message": "update project: conflicting concurrent update"}

.. list-table::
   :header-rows: 1

   * - Status
     - ``error``
     - Meaning
   * - 400
     - ``invalid_argument``
     - Malformed body or invalid value; ``message`` says which
   * - 401
     - ``unauthenticated``
     - Missing or invalid token
   * - 403
     - ``forbidden``
     - Not allowed for this caller and scope
   * - 404
     - ``not_found``
     - No such entity or endpoint
   * - 409
     - ``already_exists``
     - Key already taken
   * - 409
     - ``conflict``
     - Stale ``version``
   * - 500
     - ``internal``
     - Server error (details only in logs)

Identity
--------

``GET /api/v1/me``
   The caller as Core sees it:
   ``{"subject", "email", "name", "groups"}``.

``/me`` also returns ``bindings``: the role bindings that apply to the
caller (see below for the representation).

Roles and role bindings
-----------------------

``GET /api/v1/roles`` → ``200`` list of ``{"role", "actions"}``

Binding representation:

.. code-block:: json

   {"id": "0199…", "claim": "groups", "value": "acme-devs", "role": "engineer",
    "scope": "customer:acme", "bootstrap": false, "created_at": "…"}

``scope`` is ``organization``, ``customer:<key>`` or ``project:<key>``.

``GET /api/v1/role-bindings`` → ``200`` list
   Bindings the caller may read (``role_binding.read`` at the binding's
   customer, or organization), bootstrap bindings first.

``POST /api/v1/role-bindings`` — ``{"claim", "value", "role", "scope"}`` → ``201``
   Requires ``role_binding.manage`` at the scope. ``org-admin`` only at
   ``organization``; ``customer-admin`` not at project scope. The
   customer/project must exist (``404``); duplicates are ``409``.

``DELETE /api/v1/role-bindings/{id}`` → ``204``
   Bootstrap bindings are not stored and return ``404``.

LLM credentials
---------------

Provider API keys used by the LLM gateway
(:doc:`/architecture/decisions/0011-llm-gateway-for-credentials-and-metering`).
A customer has a default per provider; a project may override it.
Permission: ``credential.manage`` (customer admins, org admins).

Representation — the key itself is **never returned**:

.. code-block:: json

   {"provider": "anthropic", "project": "WEB", "base_url": "",
    "fingerprint": "9f86d081…1234", "updated_at": "…"}

``provider`` is ``anthropic`` (messages) or ``openai`` (OpenAI-compatible
API, used for embeddings). ``project`` is absent for the customer default.

``GET /api/v1/customers/{customer}/credentials`` → ``200`` list

``PUT /api/v1/customers/{customer}/credentials/{provider}`` — ``{"api_key", "base_url"?}`` → ``200``
   Sets or replaces the customer default.

``PUT /api/v1/projects/{project}/credentials/{provider}`` — ``{"api_key", "base_url"?}`` → ``200``
   Sets or replaces the project override.

``DELETE`` on either path → ``204``; ``404`` when nothing is set there.

Knowledge
---------

The customer's knowledge space, stored by the Knowledge service and
reached through Core
(:doc:`/architecture/decisions/0022-humans-reach-knowledge-through-core`).
Permissions: ``knowledge.read`` (all roles), ``knowledge.write``
(engineers, customer admins, org admins), at the customer.

Representation:

.. code-block:: json

   {"id": "0199…", "kind": "decision", "title": "Use SQLite first",
    "body": "Easy development.", "projects": ["WEB"], "items": ["WEB-3"],
    "version": 2, "created_by": "8751…", "updated_by": "8751…",
    "created_at": "…", "updated_at": "…"}

``kind`` is ``document``, ``decision``, ``note`` or ``debt``; ``body`` is
Markdown; ``items`` link tracker items. ``created_by``/``updated_by`` are
the subjects of the humans (or agents) who wrote it.

``GET /api/v1/customers/{customer}/knowledge/entries`` → ``200`` list
   Filters: ``kind``, ``project``, ``item``. Most recently updated first.

``POST /api/v1/customers/{customer}/knowledge/entries`` — ``{"kind", "title", "body"?, "projects"?, "items"?}`` → ``201``

``GET /api/v1/customers/{customer}/knowledge/entries/{entry}`` → ``200``

``PATCH /api/v1/customers/{customer}/knowledge/entries/{entry}`` — ``{"version", …changed fields}`` → ``200``
   Every update creates a new immutable version.

``GET /api/v1/customers/{customer}/knowledge/entries/{entry}/versions`` → ``200``
   All versions, newest first.

``GET /api/v1/customers/{customer}/knowledge/search?q=…`` → ``200``
   Hybrid search: full-text (FTS5, BM25) and semantic similarity, fused
   by reciprocal rank. Filters: ``kind``, ``project``; ``limit`` (default
   20, max 50). Result: ``{"items": [{"entry": {…}, "score": 0.0328}]}``,
   best first. Query text is treated as plain words (no search syntax).

LLM usage
---------

``GET /api/v1/projects/{project}/usage`` → ``200``
   Token usage recorded by the LLM gateway. Query: ``group_by`` =
   ``ticket`` (default) or ``model``; ``since`` (RFC 3339). Permission:
   ``tracker.read``.

   .. code-block:: json

      {"group_by": "ticket",
       "items": [{"key": "WEB-7", "requests": 2, "input_tokens": 24, "output_tokens": 10,
                  "cache_read_tokens": 6, "cache_write_tokens": 0}],
       "total": {"requests": 2, "input_tokens": 24, "output_tokens": 10,
                 "cache_read_tokens": 6, "cache_write_tokens": 0}}

Skills
------

Agent skills (:doc:`/concepts/skills`,
:doc:`/architecture/decisions/0010-central-skill-registry`). A skill is
identified by its scope and name and has an editable **draft** and
immutable **published versions**. Permissions: ``skill.read`` (all
roles), ``skill.write`` (org admins anywhere; customer admins within
their customer and its projects).

Skill representation (the draft):

.. code-block:: json

   {"id": "0199…", "scope": "project:WEB", "name": "code-review",
    "description": "Review checklist for WEB", "body": "# Review\n…",
    "files": {"scripts/check.sh": "…"}, "latest_version": 2,
    "created_at": "…", "updated_at": "…", "version": 5}

``scope`` is ``organization``, ``customer:<key>`` or ``project:<key>``;
names are 2–64 lowercase letters, digits and single hyphens and unique
per scope; ``files`` are supporting text files (clean relative paths,
not ``SKILL.md``; at most 50 files, 1 MiB in total).

``GET /api/v1/skills?scope=…`` → ``200`` list of the skills defined at exactly that scope

``POST /api/v1/skills`` — ``{"scope", "name", "description", "body"?, "files"?}`` → ``201``

``GET /api/v1/skills/{skill}`` → ``200``

``PATCH /api/v1/skills/{skill}`` — ``{"version", "description"?, "body"?, "files"?}`` → ``200``
   Edits the draft only.

``POST /api/v1/skills/{skill}/publish`` — ``{"version"}`` → ``201``
   Snapshots the draft as version ``latest_version + 1``:
   ``{"number", "description", "body", "files", "published_by", "published_at"}``.

``GET /api/v1/skills/{skill}/versions`` → ``200`` (newest first) and
``GET /api/v1/skills/{skill}/versions/{number}`` → ``200``

``GET /api/v1/projects/{project}/skills`` → ``200``
   The project's **effective skills**: published skills of the
   organization, the project's customer and the project, the most
   specific scope winning per name, with the project's pins applied:

   .. code-block:: json

      {"items": [{"name": "code-review", "skill_id": "0199…", "scope": "project:WEB",
                  "version": 1, "latest_version": 1, "pinned": false}]}

   ``problem`` explains entries that cannot be used as configured (a pin
   beyond the latest version, a pin to an unknown name); their
   ``version`` is 0.

``GET /api/v1/projects/{project}/skill-pins`` → ``200``
   The project's pins, ``{"items": [{"name": "gitflow", "version": 0,
   "disabled": true}]}``. Unlike the effective skills, this includes
   disabled skills, so a client can show and re-enable them. Requires
   ``skill.read`` on the project.

``PUT /api/v1/projects/{project}/skills/{name}/pin`` — ``{"version"?, "disabled"?}`` → ``204``
   ``version`` 0 (default) follows the latest published version; ``N``
   fixes version N; ``disabled: true`` excludes the skill from the
   project. Requires ``skill.write`` on the project.

``DELETE /api/v1/projects/{project}/skills/{name}/pin`` → ``204``

Search
------

``GET /api/v1/search?q=…`` → ``200``
   Hybrid full-text and semantic search over milestones, epics, tickets
   (title, description, acceptance criteria) and skills (name,
   description, body). Results are filtered by permission: items need
   ``tracker.read`` on their project, skills ``skill.read`` on their
   scope. Filters: ``kind`` (``item`` or ``skill``), ``project`` (its
   items plus the skills of its organization → customer → project
   chain), ``limit`` (default 20, max 50).

   .. code-block:: json

      {"items": [{"kind": "item", "ref": "WEB-1", "title": "Rate limit the public API",
                  "snippet": "429 after 100 requests per minute", "customer": "acme",
                  "project": "WEB", "score": 0.0328}]}

   The index follows Core's event log and is updated within a few seconds
   of a change. Core currently embeds with the built-in ``hash-256``
   model; gateway embeddings for Core content are a follow-up.

Customers
---------

Representation:

.. code-block:: json

   {"id": "0199…", "key": "acme", "name": "Acme", "created_at": "2026-10-01T03:40:00Z",
    "updated_at": "2026-10-01T03:40:00Z", "version": 1}

``POST /api/v1/customers`` — ``{"key", "name"}`` → ``201``
   Key: 2–32 lowercase letters, digits, single hyphens; starts with a
   letter; immutable. Organization-level permission.

``GET /api/v1/customers`` → ``200`` list

``GET /api/v1/customers/{customer}`` → ``200``

``PATCH /api/v1/customers/{customer}`` — ``{"name", "version"}`` → ``200``

Projects
--------

Representation:

.. code-block:: json

   {"id": "0199…", "key": "ACME", "customer": "acme", "name": "Acme Shop",
    "description": "Online shop", "created_at": "…", "updated_at": "…", "version": 1}

``POST /api/v1/customers/{customer}/projects`` — ``{"key", "name", "description"}`` → ``201``
   Key: 2–10 uppercase letters and digits, starts with a letter,
   globally unique, immutable; prefix of ticket keys (``ACME-42``).

``GET /api/v1/customers/{customer}/projects`` → ``200`` list

``GET /api/v1/projects/{project}`` → ``200``

``PATCH /api/v1/projects/{project}`` — ``{"name", "description", "version"}`` → ``200``

.. _reference-rest-items:

Milestones, epics and tickets
-----------------------------

All three are **items** sharing one per-project key sequence
(``WEB-1``, ``WEB-2``, …). Representation:

.. code-block:: json

   {"id": "0199…", "key": "WEB-2", "project": "WEB", "kind": "ticket",
    "title": "Export invoices", "description": "", "state": "backlog",
    "type": "feature", "acceptance_criteria": ["CSV has one row per invoice"],
    "policy": {"review_mode": "agent", "merge_mode": "auto"},
    "epic": "WEB-1", "milestone": "WEB-3",
    "created_at": "…", "updated_at": "…", "version": 1}

``stage`` appears while a ticket is ``in_progress``. ``type``,
``acceptance_criteria`` and ``policy`` exist only on tickets; ``epic``
only on tickets; ``milestone`` on tickets and epics.

.. list-table:: Field values
   :header-rows: 1

   * - Field
     - Values
   * - ``kind``
     - ``milestone``, ``epic``, ``ticket``
   * - ``type``
     - ``feature`` (default), ``bug``, ``tech_debt``, ``docs``, ``spike``
   * - ``policy.review_mode``
     - ``agent`` (default), ``agent+human``
   * - ``policy.merge_mode``
     - ``auto`` (default), ``manual``

Permissions: ``tracker.read`` to read, ``tracker.write`` to create,
update and transition (:doc:`/architecture/security`).

``POST /api/v1/projects/{project}/items`` → ``201``
   Body: ``kind``, ``title`` (required), ``description``, and for tickets
   ``type``, ``acceptance_criteria``, ``policy``, ``epic``; ``milestone``
   for tickets and epics. Related epic/milestone must be of that kind in
   the same project.

``GET /api/v1/projects/{project}/items`` → ``200`` list
   Query filters: ``kind``, ``state``, ``epic``, ``milestone``. Ordered by
   key number.

``GET /api/v1/items/{item}`` → ``200``

``PATCH /api/v1/items/{item}`` → ``200``
   Body: ``version`` plus any of ``title``, ``description``, ``type``,
   ``acceptance_criteria``, ``policy``, ``epic``, ``milestone``. An empty
   string for ``epic``/``milestone`` removes the relation.

``POST /api/v1/items/{item}/transition`` — ``{"state", "version"}`` → ``200``
   Moves the item on behalf of a human. Allowed transitions:

   .. list-table::
      :header-rows: 1

      * - From (ticket)
        - To
      * - ``backlog``
        - ``ready``, ``cancelled``, ``done``
      * - ``ready``
        - ``backlog``, ``paused``, ``cancelled``, ``done``
      * - ``in_progress``, ``waiting_for_answer``
        - ``paused``, ``cancelled``
      * - ``paused``
        - ``ready``, ``backlog``, ``cancelled``, ``done``
      * - ``done``, ``cancelled``
        - ``backlog``

   Milestones and epics: ``open`` ↔ ``done`` / ``cancelled``.
   ``in_progress`` and ``waiting_for_answer`` are set only by the
   orchestrator. Other transitions return ``400``.

``GET /api/v1/items/{item}/history`` → ``200`` list
   The item's events, oldest first:
   ``{"seq", "type", "occurred_at", "actor", "payload"}``.

Dependencies
------------

Edges between two items of the same project
(:doc:`/concepts/scheduling`). ``blocks`` edges must form an acyclic
graph; ``relates`` is undirected and informational. Seen from an item,
an edge has a direction:

.. list-table::
   :header-rows: 1

   * - ``type``
     - Meaning (from ``{item}``'s point of view)
   * - ``blocks``
     - ``{item}`` must be resolved before ``item`` can start
   * - ``blocked_by``
     - ``item`` must be resolved before ``{item}`` can start
   * - ``relates``
     - Related, no ordering

Representation: ``{"id", "type", "item": {"key", "kind", "title", "state"}}``.

``POST /api/v1/items/{item}/dependencies`` — ``{"type", "item"}`` → ``201``
   ``400`` if the edge would create a cycle, connects an item to itself or
   crosses projects; ``409`` if it already exists.

``GET /api/v1/items/{item}/dependencies`` → ``200`` list

``DELETE /api/v1/dependencies/{id}`` → ``204``

``GET /api/v1/projects/{project}/runnable`` → ``200`` list of items
   Tickets in state ``ready`` whose blockers are all resolved (``done``
   or ``cancelled``) — what the scheduler may start.

Planner sessions
----------------

Chats with the planner agent; messages are sent and streamed over the
realtime API (:ref:`reference-realtime-planner`).

``POST /api/v1/projects/{project}/planner/sessions`` — ``{"title"}`` → ``201``
   Needs ``tracker.write``. Returns ``{"id", "project", "title",
   "created_by", "created_at", "updated_at", "running"}``.

``GET /api/v1/projects/{project}/planner/sessions`` → ``200`` list, most recently active first

``GET /api/v1/planner/sessions/{session}`` → ``200``
   The session with its transcript, ``"messages": [{"seq", "role",
   "content", "author"?, "stop_reason"?, "usage", "created_at"}]``.
   ``content`` is a list of blocks: ``{"type": "text", "text"}``,
   ``{"type": "tool_use", "tool_use_id", "name", "input"}`` (assistant)
   and ``{"type": "tool_result", "tool_use_id", "text", "is_error"?}``
   (role ``user``, written by the planner). ``usage`` holds the token
   counts of assistant messages.

.. _reference-rest-changesets:

Plan changesets
---------------

A **plan changeset** is a batch of planning changes proposed by the
planner (or a person) and decided by a human, wholly or operation by
operation (:doc:`/concepts/workflow`). Proposing needs ``tracker.write``
on the project; applying and rejecting additionally need a **human**
caller, so service identities such as the planner can propose but never
approve.

An operation is one of:

.. code-block:: json

   {"kind": "create_item", "ref": "login",
    "create": {"kind": "ticket", "title": "Login", "epic": "$auth",
               "acceptance_criteria": ["OIDC sign-in works"]}}

   {"kind": "update_item",
    "update": {"item": "WEB-7", "title": "Profile page (needs login)"}}

   {"kind": "add_dependency",
    "dependency": {"from": "$login", "to": "WEB-7", "type": "blocks"}}

Item references (``epic``, ``milestone``, ``from``, ``to``) are either
the key of an existing item of the project or ``$`` followed by the
``ref`` of an **earlier** ``create_item`` operation in the same
changeset. ``create`` takes the fields of a new item
(:ref:`reference-rest-items`); ``update`` changes only the fields it
lists (``""`` for ``epic`` or ``milestone`` removes the relation).

``POST /api/v1/projects/{project}/changesets`` — ``{"title", "summary"?, "operations"}`` → ``201``
   Validates every operation against the current project as if all were
   approved (fields, references, relation kinds, no duplicate or cyclic
   dependencies, at most one update per item, at most 200 operations)
   and stores the changeset with status ``proposed``. Nothing else
   changes. ``400`` with the failing operation's number otherwise.

``GET /api/v1/projects/{project}/changesets[?status=proposed|applied|rejected]`` → ``200`` list, newest first

``GET /api/v1/changesets/{changeset}`` → ``200``
   ``{"id", "project", "title", "summary", "status", "operations",
   "proposed_by", "created_at", "decided_by"?, "decided_at"?, "approved",
   "results", "version"}``. Once applied, ``approved`` lists the applied
   operations (0-based) and ``results`` has one entry per operation:
   ``{"key"}`` for created or updated items, ``{"dependency"}`` for added
   dependencies, ``{}`` for operations that were not approved.

``POST /api/v1/changesets/{changeset}/apply`` — ``{"operations": [0, 1, 4]}`` → ``200``
   Applies exactly the listed operations in one atomic write and marks
   the changeset ``applied``. Every operation an approved one refers to
   through ``$ref`` must be approved too (``400`` otherwise). Operations
   are validated again against the current project; keys of created
   items are assigned now. ``409`` if the changeset was already decided.

``POST /api/v1/changesets/{changeset}/reject`` → ``200``
   Marks a proposed changeset ``rejected``; ``409`` if already decided.

Each decision records a ``changeset.applied`` or ``changeset.rejected``
event; created items, updates and dependencies record their usual
events, with the changeset ID in the payload.

Every create and update records one event (:doc:`/architecture/data`).
