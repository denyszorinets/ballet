ADR-0005: Knowledge as a Separate Service with an MCP Interface
===============================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Knowledge (documents, decisions, notes, debt, lineage) is the long-term
memory that makes fresh-agent onboarding work. It is the most
confidential data Ballet holds, must be isolated per customer, and is
consumed primarily by agents. It changes on a different cadence from the
tracker and may later need different storage (vector search).

Decision
--------

Knowledge is a separate Go service with its **own database**. Agents use
it through an **MCP server** (search, read, write, lineage); the UI uses
a REST API. It references tracker entities only by ID and trusts
Core-issued tokens for identity and customer scope, enforcing the scope
on every call.

Alternatives Considered
-----------------------

Module inside Core, shared database
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simpler deployment; joins between knowledge and tickets.

Disadvantages:

- Isolation depends on every Core query; storage cannot evolve
  independently; harder to place in a separate security zone.

Store knowledge in the git repository
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Versioned with code.

Disadvantages:

- No cross-project lineage; not searchable across repos; ties
  knowledge to one host.

Decision Criteria
-----------------

Security isolation, independent evolution, agent accessibility,
operational cost.

Rationale
---------

A separate service and database give a physical boundary for the most
sensitive data and let the knowledge model evolve independently. MCP is
the native interface for all supported agent runtimes.

Consequences
------------

Positive
~~~~~~~~

- Isolation enforced in one place; database can be secured separately.

Negative
~~~~~~~~

- Cross-service consistency (e.g. ticket deleted, links remain) must be
  handled explicitly.

Risks
~~~~~

- Lineage queries need both tracker and knowledge data; the boundary
  must be designed so Knowledge stores enough link data to answer them.

Follow-up
~~~~~~~~~

- Design the knowledge data model and MCP tool set.

References
----------

- :doc:`/concepts/knowledge`
- :doc:`0004-tenancy-organization-customer-project`
- :doc:`0019-sqlite-first-rqlite-later`
