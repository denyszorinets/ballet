Knowledge MCP
=============

Agents use the organization's knowledge base through MCP
(:doc:`/architecture/decisions/0005-knowledge-as-separate-service-with-mcp`):
streamable HTTP at ``/mcp`` on the Knowledge service.

Authentication
--------------

``Authorization: Bearer <run token>`` with audience ``knowledge``. The
token's organization selects the knowledge space; ``knowledge.read`` allows
the read tools, ``knowledge.write`` the write tools. Requests without a
valid token get ``401``; tool calls outside the token's capabilities fail
with a tool error. Entries are recorded with the run's subject as author.

Configuring an agent
--------------------

For Claude Code (``.mcp.json`` in the run's workspace — the agent writes
it):

.. code-block:: json

   {"mcpServers": {"ballet-knowledge": {
      "type": "http", "url": "http://knowledge:8081/mcp",
      "headers": {"Authorization": "Bearer ${BALLET_RUN_TOKEN}"}}}}

Tools
-----

.. list-table::
   :header-rows: 1

   * - Tool
     - Input
     - Result
   * - ``knowledge_search``
     - ``query``; ``kind``, ``project``, ``limit`` (default 10)
     - ``entries``: summaries (``id``, ``kind``, ``title``, ``snippet``,
       ``projects``, ``items``, ``version``, ``score``), best first —
       hybrid full-text and semantic search
   * - ``knowledge_list``
     - ``kind``, ``project``, ``item`` (e.g. ``WEB-42``), ``limit``
       (default 50)
     - ``entries``, most recently updated first
   * - ``knowledge_get``
     - ``id``
     - The full entry with ``body`` (Markdown), ``updated_by``,
       ``updated_at``
   * - ``knowledge_create``
     - ``kind`` (``document``, ``decision``, ``note``, ``debt``),
       ``title``; ``body``, ``projects`` (default: the run's project),
       ``items``
     - The created entry
   * - ``knowledge_update``
     - ``id``, ``version`` (as read); any of ``kind``, ``title``,
       ``body``, ``projects``, ``items``
     - The updated entry; a stale ``version`` is rejected, previous
       versions are kept

Trying it
---------

.. code-block:: bash

   TOKEN=$(bin/devtoken -keys data/token-keys.json -organization acme -project WEB \
     -ticket WEB-1 -aud knowledge -caps knowledge.read,knowledge.write)
   curl localhost:8081/mcp -H "Authorization: Bearer $TOKEN" \
     -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
          "params":{"name":"knowledge_search","arguments":{"query":"invoice export"}}}'
