ADR-0024: Authentication Off by Default for a Single Local User
===============================================================

:Status: Accepted
:Date: 2026-10-04

.. note::

   Since :doc:`0027-tenancy-platform-organization-project`, customers are
   called **organizations** and the installation-wide level formerly
   called the organization is the **platform**; this record keeps the
   terms of its time.

Context
-------

Ballet authenticates humans with an external OIDC provider
(:doc:`0006-oidc-claims-rbac-and-scoped-run-tokens`). That is right for an
installation shared by several people and customers, but it makes the
common first experience — one developer trying Ballet on their own
machine — depend on running and configuring an identity provider
(Keycloak needs Java and a realm). Single-user use has no need for
identities, roles or tenants beyond "me".

Decision
--------

- Authentication is enabled by configuring ``[oidc] issuer_url``. Without
  it (the default), Core runs in **local mode**: every human caller —
  REST, realtime, the web UI — is the built-in *local user* (subject
  ``local``), bound as organization admin. The SPA reads the empty issuer
  from ``/config.json`` and skips sign-in.
- In local mode Core **listens on localhost** unless ``[server] addr``
  names a host explicitly, and logs a warning at startup.
- Workloads are unaffected: run tokens, service tokens and the Runner
  protocol are always signed and verified.
- ``make run`` and the compose installation default to local mode;
  ``BALLET_AUTH=oidc`` (or an issuer) enables sign-in.

Alternatives Considered
-----------------------

Always require OIDC
~~~~~~~~~~~~~~~~~~~

Advantages: one code path; no way to run an unprotected installation.

Disadvantages: every local use needs an identity provider.

A built-in password login
~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages: works without an external provider for several users.

Disadvantages: Ballet would manage passwords, which ADR-0006 rules out;
a single local user needs no login at all.

Decision Criteria
-----------------

Ease of first use, security of shared installations, simplicity.

Rationale
---------

Making the identity provider optional removes the largest setup step for
the single-user case, while shared installations keep exactly the same
OIDC and RBAC behavior. Binding to localhost keeps an unauthenticated
Core off the network unless someone asks for it explicitly.

Consequences
------------

Positive
~~~~~~~~

- ``make run`` works without Java or Keycloak; the UI opens directly.

Negative
~~~~~~~~

- An operator can expose an unauthenticated Core by setting a public
  ``[server] addr``; the startup warning and documentation call this out.

Risks
~~~~~

- Data created in local mode belongs to the subject ``local``; switching
  an installation to OIDC later keeps that data, but role bindings must
  then be created for the real users.

References
----------

:doc:`0006-oidc-claims-rbac-and-scoped-run-tokens`,
:doc:`/how-to/configure-oidc`, :doc:`/how-to/run-locally`.
