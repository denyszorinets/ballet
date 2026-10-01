REST API
========

Core's REST API serves stateless operations
(:doc:`/architecture/decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`).
Base path: ``/api/v1``.

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

Every create and update records one event (:doc:`/architecture/data`).
