LLM Gateway
===========

All LLM traffic of agent runs goes through the gateway
(:doc:`/architecture/decisions/0011-llm-gateway-for-credentials-and-metering`):
it authenticates the run, injects the provider credential configured in
Core, and forwards the request. Agents never hold provider keys.

Anthropic API
-------------

The gateway serves the Anthropic API under ``/v1/`` (e.g.
``POST /v1/messages``), including streaming (server-sent events are
forwarded as they arrive).

Authentication
   A Ballet run token with audience ``gateway`` and capability
   ``llm.invoke``, in ``Authorization: Bearer <token>`` or ``x-api-key:
   <token>``. The token's ``cust``/``proj`` claims select the credential:
   the project's override, else the customer default
   (:doc:`rest-api`, *LLM credentials*).

Configuring an agent
   Point the agent at the gateway and give it the run token as its API
   key, e.g. for Claude Code:

   .. code-block:: bash

      ANTHROPIC_BASE_URL=http://gateway:8082
      ANTHROPIC_API_KEY=<run token>

Errors
   In the Anthropic error format
   (``{"type": "error", "error": {"type", "message"}}``):

   .. list-table::
      :header-rows: 1

      * - Status
        - ``error.type``
        - Cause
      * - 401
        - ``authentication_error``
        - Missing or invalid run token
      * - 403
        - ``permission_error``
        - Token lacks ``llm.invoke``, or no credential is configured
      * - 502
        - ``api_error``
        - Credential lookup or provider unreachable

   Errors returned by the provider itself are passed through unchanged.

Trying it locally
-----------------

``core/cmd/devtoken`` mints run tokens from Core's key file
(development only):

.. code-block:: bash

   go build -o bin/devtoken ./core/cmd/devtoken
   TOKEN=$(bin/devtoken -keys data/token-keys.json -customer acme -project WEB \
     -ticket WEB-1 -aud gateway -caps llm.invoke)
   curl localhost:8082/v1/messages -H "x-api-key: $TOKEN" \
     -H "anthropic-version: 2023-06-01" -H "content-type: application/json" \
     -d '{"model":"claude-haiku-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'
