ADR-0018: REST for Stateless, WebSocket + JSON-RPC + MessagePack for Stateful
=============================================================================

:Status: Accepted
:Date: 2026-10-01

Context
-------

Ballet's interfaces carry two kinds of traffic:

- **Stateless** operations: reading and changing tickets, projects,
  pipelines, skills, knowledge entries; queries for lists, reports and
  usage. Request/response, cacheable, easy to script.
- **Stateful** interactions: planner chat and sub-chats, live session
  output, inbox and board updates, Runner ↔ Core control (start/stop
  sessions, stream logs, report status). Long-lived, bidirectional,
  latency-sensitive, often high-volume.

Long-running unattended operation needs prompt detection of dead
connections, dead Runners and dead sessions
(:doc:`/concepts/unattended-operation`).

Decision
--------

**REST (JSON over HTTP)** for stateless operations — UI ↔ Core, UI ↔
Knowledge, and external automation.

**WebSocket carrying JSON-RPC 2.0 messages encoded with MessagePack** for
stateful interactions — UI ↔ Core and Runner ↔ Core:

- JSON-RPC requests/responses for commands (``chat.send``,
  ``session.cancel``); JSON-RPC notifications for server push
  (``session.output``, ``ticket.changed``, ``question.raised``).
- Clients **subscribe** to streams (a session's output, a project's
  board). Every stream event carries a monotonically increasing
  sequence number.
- **Heartbeats** in both directions as JSON-RPC notifications
  (``$/heartbeat``) at a fixed interval, carrying the sender's time and
  last seen sequence. A peer that misses a configurable number of
  heartbeats is considered gone; the connection is closed and
  reconnection begins. Application-level heartbeats are used because
  browsers cannot send WebSocket ping frames.
- On reconnect, the client resumes each subscription from its last
  sequence number; if the server can no longer replay that far, it
  answers *resync required* and the client reloads the snapshot via REST.
  **REST provides snapshots, WebSocket provides deltas.**
- The WebSocket subprotocol selects the encoding: ``ballet.v1.msgpack``
  (default) or ``ballet.v1.json`` (same messages as text, for debugging
  and tooling).
- Authentication: the first message carries the access token (browsers
  cannot set WebSocket headers); tokens are refreshed in-band before
  expiry.

Runner heartbeats also carry the liveness of each session it hosts;
Core uses them for stuck detection and to reconcile runs after a Runner
loses contact.

Agent-facing interfaces stay on **MCP** (its own transports); LLM traffic
stays on the providers' HTTP APIs through the gateway.

Alternatives Considered
-----------------------

REST plus Server-Sent Events
~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Simple; plain HTTP; automatic browser reconnection.

Disadvantages:

- One-directional; chat and Runner control need a second channel;
  text-only.

gRPC (streaming)
~~~~~~~~~~~~~~~~

Advantages:

- Typed contracts, efficient binary encoding, bidirectional streams.

Disadvantages:

- Needs gRPC-Web and a proxy in the browser; heavier toolchain for the
  Svelte UI.

WebSocket with JSON only
~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Human-readable on the wire.

Disadvantages:

- Larger and slower for high-volume log and output streams. Kept as the
  debugging subprotocol.

Decision Criteria
-----------------

Bidirectionality, efficiency for streaming, browser support, robustness
of long-lived connections, debuggability.

Rationale
---------

REST keeps the bulk of the API simple, cacheable and scriptable.
WebSocket covers all bidirectional traffic with one connection;
JSON-RPC gives a minimal, well-known message envelope; MessagePack keeps
streaming efficient while the JSON subprotocol preserves debuggability.
Sequence numbers and heartbeats make long-lived connections recoverable
and observable.

Consequences
------------

Positive
~~~~~~~~

- One stateful protocol for UI and Runner; fast failure detection;
  lossless resume.

Negative
~~~~~~~~

- Two API styles to document and test; stream replay requires a
  bounded event buffer (backed by the orchestration event log,
  :doc:`0016-durable-orchestration-in-the-database`).

Risks
~~~~~

- WebSocket connections through proxies and load balancers need idle
  timeouts above the heartbeat interval.

Follow-up
~~~~~~~~~

- Specify REST contracts (OpenAPI) and the JSON-RPC method catalogue.
- Choose heartbeat interval and miss threshold defaults.

References
----------

- :doc:`/architecture/integration`
