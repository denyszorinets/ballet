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
bug in Core or the UI cannot expose another customer's knowledge.

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

Proposed roles:

.. list-table::
   :header-rows: 1

   * - Role
     - Can
   * - ``org-admin``
     - Manage customers, org skills, templates, runtimes, role bindings.
   * - ``customer-admin``
     - Manage a customer's projects, credentials, customer skills.
   * - ``engineer``
     - Chat with the planner, approve changesets, set execution policy,
       review and merge.
   * - ``approver``
     - Approve changesets and milestones; review. Intended for customer
       staff.
   * - ``viewer``
     - Read the tracker, Gantt, runs and knowledge in scope.

Bindings at a broader scope apply to everything inside it. Authorization
is deny-by-default and evaluated in the application layer of Core and
Knowledge; the UI only reflects it.

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

- LLM provider credentials are stored per customer (optionally per
  project), encrypted at rest, and used only by the LLM gateway.
- Git platform credentials are stored per project. Core uses an API
  token for the forge adapter (open, review, merge pull requests); the
  Runner uses a clone credential; containers receive a credential
  limited to pushing the run's branch where the platform supports it.

Container isolation
-------------------

Runs execute in rootless containers where possible, with no access to the
host's container socket, restricted network egress (git remote, LLM
gateway, Ballet MCPs, package registries), and resource limits.
