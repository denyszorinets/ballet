Users and access
================

Locally (``make run``) there is no sign-in: you are the single user, an
platform admin. With several users, people sign in through your
identity provider (OIDC — Keycloak, Entra ID, Okta, Google, …;
:doc:`/how-to/configure-oidc`), and **Access** in the header grants
them roles.

A grant matches a **claim** of the user's sign-in token — for example
``groups`` contains ``acme-devs``, or ``email`` is
``pm@acme.example`` — and gives a **role** in a **scope**:

``platform-admin`` (platform)
   Everything, including creating customers.
``customer-admin`` (customer)
   Manages the customer, its projects, credentials, skills, access and
   runs.
``engineer`` (customer or project)
   Reads, and plans and edits tickets.
``approver``, ``viewer`` (customer or project)
   Read.

A customer grant covers all its projects; a project grant only that
project. The full table is in :doc:`/architecture/security`.
