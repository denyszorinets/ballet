Configure OIDC Authentication
=============================

Ballet does not manage passwords. Humans authenticate with an external
OpenID Connect provider; Ballet validates the provider's access tokens
(:doc:`/architecture/decisions/0006-oidc-claims-rbac-and-scoped-run-tokens`).

Requirements for the identity provider
--------------------------------------

- A **public client** for the web UI using the authorization code flow
  with PKCE (S256).
- Access tokens must contain the audience ``ballet`` (or whatever
  ``[oidc] audience`` is set to). In Keycloak, add an *Audience* protocol
  mapper to the client.
- Claims used for authorization, typically ``groups``, must be in the
  access token. In Keycloak, add a *Group Membership* mapper with
  *Full group path* off.
- ``email`` and ``name`` claims are shown in the UI when present.

Configure Core
--------------

.. code-block:: toml

   [oidc]
   issuer_url = "https://idp.example.com/realms/ballet"
   audience = "ballet"

or ``BALLET_CORE_OIDC_ISSUER_URL`` / ``BALLET_CORE_OIDC_AUDIENCE``. See
:ref:`reference-config-oidc`.

Core fetches the issuer's discovery document at startup and **refuses to
start** if the issuer is unreachable. Signing keys are cached and
refreshed automatically when the provider rotates them.

Every route under ``/api/`` requires ``Authorization: Bearer <access
token>``; requests without a valid token get ``401`` with
``WWW-Authenticate: Bearer realm="ballet"``. Operational endpoints
(``/healthz``, ``/readyz``, ``/metrics``) are not authenticated.

Check the configuration:

.. code-block:: bash

   curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/me

returns the caller as Ballet sees it:

.. code-block:: json

   {"subject": "8751…", "email": "bob@acme.test", "name": "Bob Developer",
    "groups": ["acme-devs"]}

Development identity provider
-----------------------------

The repository contains a ready Keycloak setup:

.. code-block:: bash

   make dev-keycloak    # Keycloak on http://localhost:8180, realm "ballet"
   BALLET_CORE_OIDC_ISSUER_URL=http://localhost:8180/realms/ballet go run ./core/cmd/core
   curl -H "Authorization: Bearer $(scripts/dev-token.sh bob)" localhost:8080/api/v1/me

``make dev-keycloak`` runs Keycloak's Java distribution (Java 21+);
with Docker or Podman, ``docker compose -f deploy/dev/compose.yaml up -d``
is equivalent. Test users (password = user name):

.. list-table::
   :header-rows: 1

   * - User
     - Groups
   * - ``alice``
     - ``ballet-admins``
   * - ``bob``
     - ``acme-devs``
   * - ``carol``
     - ``acme-viewers``

The realm is defined in :repo:`deploy/dev/keycloak/ballet-realm.json`.
Use ``localhost`` (not ``127.0.0.1``) everywhere: the development issuer
is ``http://localhost:8180/realms/ballet``.
