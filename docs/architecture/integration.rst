APIs and Protocols
==================

Which interface uses which protocol
(:doc:`decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`).

.. list-table::
   :header-rows: 1

   * - Link
     - Protocol
     - Carries
   * - UI → Core, UI → Knowledge
     - REST (JSON over HTTP)
     - CRUD and queries; snapshots after (re)connect
   * - UI ↔ Core
     - WebSocket, JSON-RPC 2.0, MessagePack
     - Planner chat, sub-chats, live session output, board/inbox
       updates
   * - Agent ↔ Core
     - WebSocket, JSON-RPC 2.0, MessagePack
     - Runs, human input, session events and output, results, liveness
       (:doc:`/reference/agents`)
   * - Agent → Core (tracker), Agent → Knowledge
     - MCP
     - Ticket context, progress, questions, proposals; knowledge
       search, read, write, lineage
   * - Agent → LLM gateway → provider
     - Provider HTTP APIs
     - LLM requests, metered per run
   * - Core ↔ git platform
     - Forge REST APIs and webhooks
     - Pull requests, review/CI/merge state
   * - Prometheus → services
     - HTTP ``/metrics``
     - Metrics

Stateful connections
--------------------

- **Envelope:** JSON-RPC 2.0 — requests/responses for commands,
  notifications for server push.
- **Encoding:** WebSocket subprotocol ``ballet.v1.msgpack`` (default) or
  ``ballet.v1.json`` (debugging).
- **Authentication:** the first message carries the access token (OIDC
  for humans, a service token for agents); refreshed in-band.
- **Subscriptions:** clients subscribe to streams; every event has a
  sequence number.
- **Heartbeats:** ``$/heartbeat`` notifications in both directions at a
  fixed interval; missing several closes the connection.
- **Resume:** on reconnect, subscriptions resume from the last sequence
  number, or the server requests a resync and the client reloads the
  snapshot over REST.

.. mermaid::

   sequenceDiagram
     participant C as Client (UI)
     participant S as Core
     C->>S: WebSocket connect (ballet.v1.msgpack)
     C->>S: auth(token)
     C->>S: subscribe(session 42, from_seq=0)
     loop every interval
       C-->>S: $/heartbeat
       S-->>C: $/heartbeat
     end
     S-->>C: session.output (seq 1..n)
     Note over C,S: connection lost
     C->>S: reconnect + auth
     C->>S: subscribe(session 42, from_seq=n)
     S-->>C: session.output (seq n+1..) or resync_required
