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
- Notifications are sent and must be processed **in order** (stream
  events depend on it); requests may be processed concurrently.

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
   * - ``stream.subscribe``
     - client → server
     - Subscribe to a change stream, see below
   * - ``stream.unsubscribe``
     - client → server
     - ``{"subscription"}``
   * - ``stream.event``
     - server → client
     - One event on a subscription
   * - ``stream.closed``
     - server → client
     - The server ended a subscription

Change streams
--------------

Streams carry the events of Core's event log (:doc:`/architecture/data`)
as they are recorded:

.. list-table::
   :header-rows: 1

   * - Stream
     - Events
     - Permission
   * - ``project:<KEY>``
     - Everything in the project: items, dependencies, …
     - ``tracker.read`` on the project
   * - ``item:<KEY>``
     - Changes of one item
     - ``tracker.read`` on its project

``stream.subscribe`` — ``{"stream", "from_seq"?}`` → ``{"subscription", "seq"}``
   ``seq`` is the event log position at subscription time. Without
   ``from_seq``, only events after ``seq`` are delivered. With
   ``from_seq``, the server first replays the stream's events after
   ``from_seq``, then continues live — no gaps, no duplicates. If more
   than 1000 events would have to be replayed, it answers error
   ``-32010`` (*resync required*).

``stream.event`` notification:

.. code-block:: json

   {"subscription": "s1", "seq": 1042, "type": "item.state_changed",
    "entity_type": "item", "entity_id": "0199…", "entity_key": "WEB-42",
    "occurred_at": 1790830000000,
    "actor": {"kind": "human", "subject": "8751…"},
    "payload": {"from": "backlog", "to": "ready"}}

``stream.closed`` — ``{"subscription", "reason"}``: reason ``lagging``
means the client did not keep up; resubscribe with ``from_seq`` = last
received ``seq``.

**Client recipe** (what the web UI does):

#. ``stream.subscribe`` without ``from_seq``; remember the returned
   ``seq``.
#. Load the snapshot over REST.
#. Apply ``stream.event`` notifications, remembering the last ``seq``
   (events may already be in the snapshot; apply idempotently, e.g. by
   refetching the entity or comparing ``version``).
#. After a reconnect, resubscribe with ``from_seq`` = last ``seq``; on
   ``-32010``, start again at step 1.

Events reach subscribers within about 200 ms of being recorded.

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
   * - ``-32010``
     - Resync required (``from_seq`` too old)

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
