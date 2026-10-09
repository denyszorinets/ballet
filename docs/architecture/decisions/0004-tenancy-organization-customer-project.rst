ADR-0004: Tenancy Model — Organization, Customer, Project
=========================================================

:Status: Superseded by :doc:`0027-tenancy-platform-organization-project`
:Date: 2026-10-01

Context
-------

Ballet is intended to run an AI development shop: one operator serving
many customers, each with several projects. Customer data, and in
particular customer knowledge, must be legally isolated between
customers. Some resources (engineering standards, devcontainer templates)
are shared across the whole shop.

Decision
--------

A single Ballet installation has one **Organization**. The organization
has **Customers**; customers have **Projects**.

- The customer is the hard isolation boundary for tickets, runs,
  knowledge, credentials and customer-scoped skills.
- Knowledge is scoped to the customer: all projects of a customer share
  one knowledge space.
- Organization-wide resources: skill registry (org scope), devcontainer
  templates, agent runtime definitions, role bindings.

Multiple organizations per installation (SaaS multi-tenancy) is out of
scope.

Alternatives Considered
-----------------------

Project as the isolation boundary
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Finest isolation.

Disadvantages:

- Knowledge cannot flow between a customer's own projects, losing
  cross-project lineage that customers expect.

Multi-organization SaaS
~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Could serve many shops.

Disadvantages:

- Adds a tenancy level nobody currently needs.

Decision Criteria
-----------------

Legal isolation, knowledge reuse within a customer, simplicity.

Rationale
---------

The customer matches the legal boundary (contracts, NDAs) while still
allowing a customer's projects to share knowledge.

Consequences
------------

Positive
~~~~~~~~

- One clear boundary to enforce and test.

Negative
~~~~~~~~

- No sub-isolation within a customer.

Risks
~~~~~

- A customer may need isolation between its own teams. Mitigation: keep
  space as a separate concept in the Knowledge data model so a customer
  can later have more than one space.

Follow-up
~~~~~~~~~

- Automated isolation tests across customers in Core and Knowledge.

References
----------

- :doc:`/concepts/domain-model`
- :doc:`/architecture/security`
