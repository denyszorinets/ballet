Changing the REST API
=====================

Core's REST API has an explicit contract: :repo:`core/api/openapi.yaml`.
The server, its tests and the web client are all checked against it.

How the contract is enforced
----------------------------

- **Route coverage**: ``httpapi.Register`` returns every route it
  registers; a test fails unless that set equals the operations in the
  spec.
- **Response and request validation**: every REST test in
  ``core/internal/transport/httpapi`` runs through a middleware
  (kin-openapi) that validates each response, and each request the server
  accepted, against the spec. Schemas use
  ``additionalProperties: false``, so an undocumented field fails the
  tests.
- **Generated client**: ``web/src/lib/api/schema.d.ts`` is generated
  from the spec (openapi-typescript) and used through ``openapi-fetch``
  (``web/src/lib/api/client.ts``). ``make api-check`` (part of
  ``make web-check`` and CI) fails when it is stale.

Workflow
--------

#. Change ``core/api/openapi.yaml`` together with the handler.
#. Cover the new behaviour in the REST tests (they validate against the
   spec automatically).
#. ``make api-generate`` and commit ``web/src/lib/api/schema.d.ts``.
#. Update :doc:`/reference/rest-api` when the change affects users.
#. ``make check``.

In YAML flow mappings (``{ … }``), quote descriptions that contain
commas.
