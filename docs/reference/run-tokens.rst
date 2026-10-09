Run Tokens
==========

Workloads — agent runs, agents, planner sessions and Ballet services —
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
     - Workload: ``run:<id>``, ``service:agent``, ``planner:<session>``, …
   * - ``aud``
     - Services that accept the token: ``core``, ``knowledge``,
       ``gateway``
   * - ``exp``, ``iat``, ``jti``
     - Expiry, issue time, unique id
   * - ``kind``
     - ``run``, ``agent``, ``planner`` or ``service``
   * - ``cust``, ``proj``
     - Organization and project scope (required for ``run`` and ``planner``)
   * - ``tkt``
     - Ticket (required for ``run``; for ``planner`` when it acts for no
       human, e.g. answering an agent's question)
   * - ``sess``
     - Planner session (required for ``planner``)
   * - ``act.sub``
     - Human a planner acts for (RFC 8693 actor claim; required for
       ``planner`` without ``tkt``)
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
     - Search and read the organization's knowledge
   * - ``knowledge.write``
     - Create and update knowledge entries
   * - ``llm.invoke``
     - Call the LLM gateway
   * - ``agent.connect``
     - Connect an agent to Core
   * - ``credentials.read``
     - (gateway) Read LLM credentials from Core
   * - ``usage.write``
     - (gateway) Report LLM usage to Core
   * - ``llm.embed``
     - Compute embeddings through the gateway

A service accepts a token only if its own name is in ``aud``, and checks
both the scope (``cust``/``proj``/``tkt``) and the capability for every
operation.

.. _reference-run-tokens-services:

Service tokens
--------------

Ballet's own services authenticate to each other with ``kind: service``
tokens that Core issues at startup and re-issues every quarter of their
lifetime (``[services] token_ttl``, default 30 days). Core writes one file
per service into ``[services] tokens_dir`` (mode ``0600``):

.. list-table::
   :header-rows: 1

   * - File
     - Subject
     - Audience
     - Capabilities
   * - ``gateway.token``
     - ``service:gateway``
     - ``core``
     - ``credentials.read``, ``usage.write``
   * - ``knowledge.token``
     - ``service:knowledge``
     - ``core``, ``gateway``
     - ``llm.embed``
   * - ``agent.token``
     - ``service:agent``
     - ``core``
     - ``agent.connect``

Services read their file with ``runtoken.FileSource`` (re-read when it
changes). In a single-host installation the directory is shared with the
services; elsewhere it must be distributed as a secret.

Core's **internal API** (``/internal/v1/``) accepts only service tokens
and checks a capability per endpoint; ``GET /internal/v1/whoami``
returns the caller's subject and capabilities.

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
