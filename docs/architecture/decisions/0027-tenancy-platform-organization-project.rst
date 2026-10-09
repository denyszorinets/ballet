ADR-0027: Tenancy Model — Platform, Organization, Project
=========================================================

:Status: Accepted
:Date: 2026-10-09
:Supersedes: :doc:`0004-tenancy-organization-customer-project`

Context
-------

:doc:`0004-tenancy-organization-customer-project` modelled one
installation as one **organization** (the operating shop) serving
**customers**, each with projects. The customer is the isolation
boundary.

That vocabulary assumes a single shop selling to clients. Ballet is also
run by independent teams and companies sharing one installation, and
each of them thinks of itself as an *organization*, not as somebody's
customer. The top-level entity a person works in should carry that name.
The installation-wide level still exists — its operators, shared skills,
the global pause and kill switch — but it is not a tenant.

Decision
--------

A Ballet installation is the **Platform**. The platform hosts many
**Organizations**; organizations have **Projects**.

- The **organization** (formerly *customer*) is the hard isolation
  boundary for tickets, runs, knowledge, credentials, budgets and
  organization-scoped skills. All projects of an organization share one
  knowledge space.
- The **platform** (formerly *organization*) holds what is installation
  wide: platform-scoped skills, role bindings at platform scope (role
  ``platform-admin``, formerly ``org-admin``), the global pause and kill
  switch, agent pools.
- Roles: ``platform-admin`` (platform scope), ``organization-admin``
  (formerly ``customer-admin``), ``engineer``, ``approver``, ``viewer``.
- The rename is complete: domain, database, REST paths
  (``/api/v1/organizations/{organization}``), run-token claims, service
  headers, metrics labels, configuration, UI and documentation. Nothing
  was released before it, so no aliases are kept; database migrations
  rewrite existing data.

Alternatives Considered
-----------------------

Rename in the UI and documentation only
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Small change.

Disadvantages:

- Two vocabularies forever: the API, data, logs and metrics would say
  *customer* while people say *organization*.

Keep the installation level as "Organization", call tenants something else
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- No change to the installation level.

Disadvantages:

- The name people expect for their tenant stays taken by a level they
  never see.

No named installation level
~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- One level fewer.

Disadvantages:

- Shared skills, platform operators and the global kill switch still
  need a scope; leaving it unnamed makes authorization harder to read.

Decision Criteria
-----------------

Matching how users name their tenant, one vocabulary across all layers,
keeping the isolation guarantees of ADR-0004 unchanged.

Rationale
---------

Only names change: the three levels, the isolation boundary and the
authorization rules stay as ADR-0004 and
:doc:`0006-oidc-claims-rbac-and-scoped-run-tokens` define them. A
complete rename before the first release is cheaper than carrying two
vocabularies.

Consequences
------------

Positive
~~~~~~~~

- Users work in *organizations*; operators administer the *platform*.
- One vocabulary in code, data, API, metrics and documentation.

Negative
~~~~~~~~

- Breaking change for API clients, configuration
  (``rbac.bootstrap_platform_admins``), dashboards and scripts.

Risks
~~~~~

- A missed rename leaves mixed vocabulary. Mitigation: the rename is
  checked by searching the repository for the old terms.

References
----------

- :doc:`/concepts/domain-model`
- :doc:`/architecture/security`
- :issue:`188`
