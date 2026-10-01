Run Tokens
==========

Workloads — agent runs, Runners, planner sessions and Ballet services —
authenticate with short-lived **run tokens** issued by Core
(:doc:`/architecture/decisions/0006-oidc-claims-rbac-and-scoped-run-tokens`).
Humans never use run tokens.

Format
------

JWT signed with **Ed25519** (``alg: EdDSA``), ``kid`` header naming the
signing key, ``iss: "ballet-core"``.

.. list-table::
   :header-rows: 1

   * - Claim
     - Meaning
   * - ``sub``
     - Workload: ``run:<id>``, ``runner:<id>``, ``planner:<session>``, …
   * - ``aud``
     - Services that accept the token: ``core``, ``knowledge``,
       ``gateway``
   * - ``exp``, ``iat``, ``jti``
     - Expiry, issue time, unique id
   * - ``kind``
     - ``run``, ``runner``, ``planner`` or ``service``
   * - ``cust``, ``proj``
     - Customer and project scope (required for ``run`` and ``planner``)
   * - ``tkt``
     - Ticket (required for ``run``)
   * - ``sess``
     - Planner session (required for ``planner``)
   * - ``act.sub``
     - Human a planner acts for (RFC 8693 actor claim; required for
       ``planner``)
   * - ``caps``
     - Capabilities, see below

Capabilities
------------

.. list-table::
   :header-rows: 1

   * - Capability
     - Grants
   * - ``tracker.read``
     - Read the token's own ticket and plan context
   * - ``tracker.report``
     - Report progress, stage reports, questions, assumptions, proposals
   * - ``knowledge.read``
     - Search and read the customer's knowledge
   * - ``knowledge.write``
     - Create and update knowledge entries
   * - ``llm.invoke``
     - Call the LLM gateway
   * - ``runner.connect``
     - Connect a Runner to Core

A service accepts a token only if its own name is in ``aud``, and checks
both the scope (``cust``/``proj``/``tkt``) and the capability for every
operation.

Keys and verification
---------------------

- Core keeps its signing keys in the file set by
  :ref:`reference-config-tokens` (created on first start, mode ``0600``).
  Back it up with the Core database; losing it invalidates all running
  sessions' tokens.
- Public keys are published, unauthenticated, at
  ``GET /.well-known/jwks.json`` on Core.
- Other services verify with ``kit/auth/runtoken.NewRemoteVerifier``:
  keys are fetched lazily, cached, and refetched when a token names an
  unknown key (throttled for repeated unknown keys).
- Rotation makes a new key active while the previous key keeps verifying
  for a retirement period that must exceed the longest token lifetime.
- Clock skew of up to 30 seconds is tolerated.
