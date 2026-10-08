Security
========

Tenancy and isolation
---------------------

The **customer** is the isolation boundary. Every customer-owned record
(projects, tickets, runs, knowledge, credentials, customer-scoped
skills) carries its customer ID, and every query is filtered by the
customer scope derived from the caller's identity — never from request
parameters alone.

Knowledge isolation is enforced in the Knowledge service itself, so a
bug in Core or the UI cannot expose another customer's knowledge. Its
isolation test suite (``knowledge/internal/isolation``) runs in CI and
covers REST, search and MCP with two customers holding near-identical
content: foreign tokens on foreign spaces (403), foreign entry IDs inside
the caller's own space (404, no content returned), cross-customer search
results (none), and forged tokens — wrong audience, untrusted signing
key, expired, missing customer or capability. Any new Knowledge endpoint
or tool must be added to it.

Human identity: OIDC
--------------------

Ballet does not manage passwords. Humans log in through an external
OIDC identity provider
(:doc:`decisions/0006-oidc-claims-rbac-and-scoped-run-tokens`).

Authorization is a set of **role bindings**: a claim match, a role, and a
scope.

.. code-block:: text

   claim groups contains "acme-devs"  →  engineer        @ customer:acme
   claim groups contains "ballet-ops" →  org-admin       @ organization
   claim email = "pm@acme.example"    →  approver        @ project:ACME

A binding matches when the token claim equals the value, or — for list
claims such as ``groups`` — contains it.

Roles and the actions they grant (more actions are added as features
arrive):

.. list-table::
   :header-rows: 1

   * - Role
     - Grants
     - Bindable at
   * - ``org-admin``
     - Every action, including creating customers
     - organization only
   * - ``customer-admin``
     - Read and update the customer; create, read, update its projects;
       manage and read role bindings within the customer; read and write
       its tracker and knowledge; manage its LLM credentials; edit and
       publish its skills; queue and cancel runs by hand (``run.manage``)
     - customer
   * - ``engineer``
     - Read customer and projects; read and write the tracker
       (milestones, epics, tickets)
     - customer, project
   * - ``approver``
     - Read customer, projects and tracker (approvals as they are added)
     - customer, project
   * - ``viewer``
     - Read customer, projects and tracker
     - customer, project

Scope semantics:

- An organization binding applies everywhere.
- A customer binding applies to the customer and all its projects.
- A project binding applies to that project, and lets the holder *see*
  (read) the project's customer — nothing else of it.
- Organization-level actions (creating customers, organization-scope
  bindings) require an organization binding, so a customer admin cannot
  escalate.

Evaluation is deny-by-default in Core's application layer; list
endpoints return only what the caller may read. Workload identities (run
tokens) are never authorized through role bindings.

**Bootstrap.** ``[rbac] bootstrap_org_admins`` grants ``org-admin`` to
claim matchers from configuration (e.g. ``groups:ballet-admins``), so a
fresh installation has an administrator. Bootstrap bindings are listed
but cannot be deleted through the API.

Agent identity: scoped run tokens
---------------------------------

Agents are not OIDC users. When a run (or planner session) starts, Core
mints a **short-lived token** bound to:

- customer, project and ticket (or planner session);
- the capabilities of that run: read its ticket and plan context,
  report progress, propose work, read/write knowledge in its customer
  space, call the LLM gateway.

Tokens expire with the run. Planner tokens additionally record the human
on whose behalf the planner acts; audit entries show both.

Secrets
-------

- LLM provider credentials are stored in Core per customer (default) and
  optionally per project (override), encrypted with AES-256-GCM using
  the key in ``[secrets] key_file``; each ciphertext is bound to its
  scope. The REST API accepts keys but only ever returns a fingerprint;
  events record the fingerprint only. The decrypted key is returned
  solely by ``/internal/v1/credentials/resolve`` to services holding
  ``credentials.read`` (the gateway) — agent sessions never receive
  provider keys.
- Git platform credentials are stored per project. Core uses an API
  token for the forge adapter (open, review, merge pull requests); a
  session receives the git token with its run's start only, in its
  environment, to clone and push the run's branch.

Session isolation
-----------------

Ballet creates no containers and needs no container socket
(:doc:`decisions/0025-agent-fleet-runs-sessions-as-processes`). The
container or VM an agent runs in is the isolation boundary:

- sessions run as an unprivileged session user, so they cannot read the
  agent's token or signal the agent; each gets a fresh workspace and
  ``HOME``, removed afterwards, and a minimal environment;
- later sessions run where earlier ones ran: run agents of different
  customers in different pools;
- coding agents skip their permission prompts (``IS_SANDBOX=1``,
  ``"permission": "allow"``) because the container is the boundary;
- restrict the containers' network egress to the git remote, the LLM
  gateway, Ballet's MCP endpoints and package registries, and give them
  resource limits (Kubernetes, compose or the VM's firewall).
