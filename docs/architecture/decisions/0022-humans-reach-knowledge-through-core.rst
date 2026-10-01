ADR-0022: Humans Reach Knowledge Through Core
=============================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Knowledge is a separate service with its own database
(:doc:`0005-knowledge-as-separate-service-with-mcp`) that must enforce
customer isolation. Two kinds of callers use it:

- agents, with run tokens scoped to a customer and project;
- humans in the web UI, authenticated with OIDC and authorized through
  role bindings that live in Core (:doc:`0006-oidc-claims-rbac-and-scoped-run-tokens`).

Duplicating RBAC (bindings, roles, scope rules) in Knowledge would create
two authorities that can drift; exposing OIDC tokens to Knowledge would
make it depend on the identity provider and on Core's binding data.

Decision
--------

- **Agents** call Knowledge directly (MCP, :issue:`73`) with their run
  token; Knowledge enforces the token's customer scope and capabilities
  (``knowledge.read``, ``knowledge.write``).
- **Humans** call Core at ``/api/v1/customers/{customer}/knowledge/...``.
  Core authorizes the request with its RBAC (actions ``knowledge.read`` for
  reads, ``knowledge.write`` for changes, at the customer scope) and
  forwards it to Knowledge with a short-lived (5 minutes) token it mints
  for the request: kind ``service``, subject ``service:core``, ``act`` =
  the human, ``cust`` = the customer, capabilities = what the human was
  granted. Knowledge never sees OIDC tokens.
- Knowledge serves the same paths without the ``/api`` prefix
  (``/v1/customers/{customer}/knowledge/...``), and rejects any request
  whose path customer differs from the token's customer.
- Core's OpenAPI document describes these paths; it is the contract for
  both services.

Alternatives Considered
-----------------------

Knowledge validates OIDC tokens and asks Core to authorize
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No proxy hop for UI requests.

Disadvantages:

- Knowledge depends on the IdP and on a Core authorization API; two
  places evaluate access; the UI talks to two origins (CORS, two clients).

RBAC replicated into Knowledge
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Knowledge decides alone.

Disadvantages:

- Two copies of bindings and policy that can drift — exactly the kind of
  inconsistency that leaks data.

Decision Criteria
-----------------

Single source of authorization truth, isolation, simplicity for the UI.

Rationale
---------

Core already authorizes every human request; forwarding with a minimal,
short-lived, customer-scoped token keeps Knowledge's trust model to one
rule — "accept only Core-issued tokens and stay inside their customer" —
which is easy to test exhaustively.

Consequences
------------

Positive
~~~~~~~~

- One RBAC implementation; one origin for the UI; Knowledge's isolation
  is enforced by token scope alone.

Negative
~~~~~~~~

- Every UI knowledge request has an extra hop through Core.

Risks
~~~~~

- Core minting overly broad tokens; mitigated by granting only the
  authorized capability and a 5 minute lifetime, and covered by tests.

References
----------

- :doc:`0005-knowledge-as-separate-service-with-mcp`
- :doc:`/reference/run-tokens`
