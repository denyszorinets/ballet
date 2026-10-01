Realtime API
============

Core's stateful API: JSON-RPC 2.0 over WebSocket
(:doc:`/architecture/decisions/0018-rest-for-stateless-websocket-json-rpc-msgpack-for-stateful`,
:doc:`/architecture/integration`). Implemented by ``kit/rpc``.

Endpoint
--------

``GET /rpc`` on Core, upgraded to WebSocket. The client must offer one of
these subprotocols (``Sec-WebSocket-Protocol``):

.. list-table::
   :header-rows: 1

   * - Subprotocol
     - Frames
     - Use
   * - ``ballet.v1.msgpack``
     - binary, MessagePack
     - default
   * - ``ballet.v1.json``
     - text, JSON
     - debugging and tooling

Without a supported subprotocol the server closes the connection with
status ``1008`` (policy violation).

Messages
--------

Every message is a JSON-RPC 2.0 envelope (a map with the same keys in
MessagePack):

.. code-block:: json

   {"jsonrpc": "2.0", "id": 7, "method": "system.ping", "params": {}}
   {"jsonrpc": "2.0", "id": 7, "result": {"time": 1790830000000, "subject": "8751…"}}
   {"jsonrpc": "2.0", "id": 8, "error": {"code": -32601, "message": "method x not found"}}
   {"jsonrpc": "2.0", "method": "$/heartbeat", "params": {"time": 1790830000000}}

- Requests have an integer ``id``; responses carry the same ``id``.
- Notifications have no ``id`` and get no response.
- The connection is symmetric: the server also sends requests and
  notifications to the client.

Authentication
--------------

The **first message** must be an ``auth`` request:

.. code-block:: json

   {"jsonrpc": "2.0", "id": 1, "method": "auth", "params": {"token": "<OIDC access token>"}}

Result: ``{"subject": "…", "expires_at": <Unix seconds>}``. Without a
valid ``auth`` request within 10 seconds, or with an invalid token, the
server closes the connection with status ``4001``.

Before ``expires_at``, the client sends ``auth.refresh`` with a new token
for the **same subject** (``kit/rpc`` clients do this automatically at
80 % of the token lifetime). When the token expires without refresh, the
server closes the connection with status ``4001``.

Heartbeats
----------

Both sides send ``$/heartbeat`` notifications (``{"time": <Unix ms>}``)
every 15 seconds. A side that receives nothing from its peer for three
intervals closes the connection with status ``4000``; clients then
reconnect. Browsers cannot send WebSocket ping frames, which is why
heartbeats are application messages.

Methods
-------

.. list-table::
   :header-rows: 1

   * - Method
     - Direction
     - Description
   * - ``auth``, ``auth.refresh``
     - client → server
     - See above
   * - ``$/heartbeat``
     - both
     - Liveness notification
   * - ``system.ping``
     - client → server
     - Returns ``{"time", "subject"}``; checks the connection end to end

Subscriptions to change streams are added by the realtime subscription
layer.

Error codes
-----------

.. list-table::
   :header-rows: 1

   * - Code
     - Meaning
   * - ``-32700`` / ``-32600``
     - Parse error / invalid request
   * - ``-32601``
     - Method not found
   * - ``-32602``
     - Invalid params
   * - ``-32603``
     - Internal error (details only in server logs)
   * - ``-32001``
     - Unauthenticated
   * - ``-32003``
     - Forbidden
   * - ``-32004``
     - Not found
   * - ``-32009``
     - Conflict

Close codes
-----------

.. list-table::
   :header-rows: 1

   * - Code
     - Meaning
   * - ``1000``
     - Normal closure
   * - ``1008``
     - No supported subprotocol
   * - ``4000``
     - Heartbeat timeout
   * - ``4001``
     - Unauthenticated: missing/invalid ``auth`` or expired token
