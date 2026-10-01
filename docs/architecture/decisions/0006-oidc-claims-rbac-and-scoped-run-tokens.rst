ADR-0006: OIDC Claims-Based RBAC for Humans, Scoped Run Tokens for Agents
=========================================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Humans from the shop and possibly from customers access Ballet with
different rights per customer and project. The shop already has, or will
have, an identity provider. Agents also act on Ballet (tracker and
knowledge MCP, LLM gateway) and must be confined to the customer,
project and ticket they work on.

Decision
--------

**Humans** authenticate exclusively through OIDC. Ballet stores no
passwords. Authorization is a list of role bindings
``(claim matcher, role, scope)`` where scope is organization, customer or
project. Bindings at a broad scope apply to everything inside it.
Evaluation is deny-by-default in the application layer.

**Agents** receive short-lived tokens minted by Core per run or planner
session, bound to customer, project, ticket/session and an explicit
capability list. Knowledge and the LLM gateway validate these tokens
and enforce their scope. Planner tokens record the human on whose behalf
the planner acts.

Alternatives Considered
-----------------------

Ballet-managed users and roles
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No IdP dependency.

Disadvantages:

- Password management; duplicated identity administration.

Agents as OIDC service accounts
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- One identity mechanism.

Disadvantages:

- Long-lived credentials in containers; IdP cannot express per-ticket
  scope.

Decision Criteria
-----------------

Security, administrative effort, least privilege for agents.

Rationale
---------

Claims-based bindings let the IdP remain the source of group membership
while Ballet owns the meaning of roles. Per-run tokens give agents least
privilege and automatic expiry.

Consequences
------------

Positive
~~~~~~~~

- Customer staff can be onboarded by adding IdP groups and bindings.

Negative
~~~~~~~~

- Local development needs an IdP (e.g. Keycloak or Dex in compose).

Risks
~~~~~

- Claim formats differ between IdPs; matchers must be flexible.

Follow-up
~~~~~~~~~

- Choose token format and signing (JWT, key rotation).
- Define the capability list.

References
----------

- :doc:`/architecture/security`
