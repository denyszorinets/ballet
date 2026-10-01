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

Embeddings
----------

``POST /v1/embeddings`` is OpenAI-compatible
(``{"model", "input": string | [string]}`` →
``{"object": "list", "model", "data": [{"index", "embedding"}], "usage"}``)
and serves hybrid search (:doc:`/architecture/decisions/0021-hybrid-vector-search-over-all-content`).

Models
   ``hash-256`` (default, ``[embeddings] default_model``) is computed by
   the gateway itself: deterministic feature hashing of words and word
   pairs into 256 dimensions. It needs no provider and captures lexical
   overlap only — suitable for development, tests and offline use. Any
   other model is forwarded to the customer's ``openai`` credential
   (OpenAI or any OpenAI-compatible API via ``base_url``).

Callers
   Run tokens with ``llm.invoke`` (scope from the token), or service
   tokens with ``llm.embed`` (Knowledge, Core indexing), which must name
   the customer in ``X-Ballet-Customer`` (and optionally
   ``X-Ballet-Project``) for credentials and metering. ``kit/embed``
   provides the ``Gateway`` client and the ``Hash`` embedder.

Usage metering
--------------

The gateway reads token usage from every response — the ``usage`` object
of JSON responses, or ``message_start`` / ``message_delta`` events of
streams, without buffering them — and attributes it to the run token's
customer, project and ticket:

- **Prometheus** (gateway ``/metrics``): ``ballet_llm_tokens_total``
  (labels ``customer``, ``project``, ``model``, ``token_type`` =
  ``input|output|cache_read|cache_write``) and
  ``ballet_llm_requests_total`` (``customer``, ``project``, ``model``,
  ``status``).
- **Core**: records are delivered in batches every 2 seconds to
  ``POST /internal/v1/usage`` (``usage.write``) and kept while Core is
  unreachable (up to 100 000). Per-ticket and per-model totals are
  available at ``GET /api/v1/projects/{project}/usage``
  (:doc:`rest-api`).

Trying it locally
-----------------

``core/cmd/devtoken`` mints run tokens from Core's key file, and
``gateway/cmd/fake-anthropic`` serves a fake Messages API with usage
(both development only). Point a project credential at the fake
(``base_url`` ``http://127.0.0.1:9900``, key ``sk-fake``) to test without
a real provider:

.. code-block:: bash

   go build -o bin/devtoken ./core/cmd/devtoken
   TOKEN=$(bin/devtoken -keys data/token-keys.json -customer acme -project WEB \
     -ticket WEB-1 -aud gateway -caps llm.invoke)
   curl localhost:8082/v1/messages -H "x-api-key: $TOKEN" \
     -H "anthropic-version: 2023-06-01" -H "content-type: application/json" \
     -d '{"model":"claude-haiku-4-5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'
