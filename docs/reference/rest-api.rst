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

Every create and update records one event (:doc:`/architecture/data`).
