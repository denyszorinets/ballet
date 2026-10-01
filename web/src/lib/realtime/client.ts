import { readable, type Readable } from 'svelte/store';
import {
	codecFor,
	SUBPROTOCOL_JSON,
	SUBPROTOCOL_MSGPACK,
	type Codec,
	type Envelope
} from './codec';

/** Connection status. */
export type Status = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'unauthenticated' | 'closed';

/** Error returned by the server for a request. */
export class RpcError extends Error {
	constructor(
		readonly code: number,
		message: string
	) {
		super(message);
		this.name = 'RpcError';
	}
}

export const CODE_RESYNC_REQUIRED = -32010;

/** An event delivered on a stream (Core's stream.event notification). */
export interface StreamEvent {
	subscription: string;
	seq: number;
	type: string;
	entity_type?: string;
	entity_id?: string;
	entity_key?: string;
	occurred_at?: number;
	actor?: { kind: string; subject: string; acting_for?: string };
	payload?: Record<string, unknown>;
}

export interface StreamHandlers {
	/** Called for every event, in order, exactly once per seq. */
	onEvent: (e: StreamEvent) => void;
	/**
	 * Called when missed events cannot be replayed: reload the snapshot.
	 * The subscription then continues with new events.
	 */
	onResync?: () => void;
}

export interface RealtimeOptions {
	/** ws:// or wss:// URL of /rpc. */
	url: string;
	/** Current access token; undefined stops reconnecting (logged out). */
	getToken: () => Promise<string | undefined>;
	/** Subprotocols to offer, preferred first (default: msgpack, json). */
	protocols?: string[];
	/** Must match the server (default 15 s). */
	heartbeatIntervalMs?: number;
	/** Reconnect after this many silent intervals (default 3). */
	heartbeatMisses?: number;
	/** Reconnect backoff (default 500 ms doubling to 30 s, with jitter). */
	backoff?: { initialMs: number; maxMs: number };
	/** Called for notifications other than heartbeats and stream events. */
	onNotification?: (method: string, params: unknown) => void;
	/** WebSocket implementation (tests). */
	WebSocket?: typeof WebSocket;
}

interface Pending {
	resolve: (v: unknown) => void;
	reject: (e: Error) => void;
}

/** A live subscription; survives reconnects. */
export class Subscription {
	/** Last delivered seq; resubscriptions resume from here. */
	lastSeq: number | undefined;
	serverId: string | undefined;
	closed = false;

	constructor(
		private readonly client: RealtimeClient,
		readonly stream: string,
		readonly handlers: StreamHandlers
	) {}

	/** Stops the subscription. */
	unsubscribe(): void {
		this.client.removeSubscription(this);
	}
}

/**
 * Client for Core's realtime API (JSON-RPC over WebSocket, see
 * docs/reference/realtime-api.rst): authentication with refresh,
 * heartbeats, reconnect with backoff, and stream subscriptions that resume
 * without gaps after reconnecting.
 */
export class RealtimeClient {
	private opts: Required<Omit<RealtimeOptions, 'onNotification'>> &
		Pick<RealtimeOptions, 'onNotification'>;
	private ws: WebSocket | undefined;
	private codec: Codec | undefined;
	private nextId = 0;
	private nextSubId = 0;
	private pending = new Map<number, Pending>();
	private waiting: Array<() => void> = [];
	private subs = new Set<Subscription>();
	private byServerId = new Map<string, Subscription>();
	private lastSeen = 0;
	private heartbeatTimer: ReturnType<typeof setInterval> | undefined;
	private refreshTimer: ReturnType<typeof setTimeout> | undefined;
	private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
	private attempt = 0;
	private stopped = true;
	private listeners = new Set<(s: Status) => void>();
	private handlers = new Map<string, Set<(params: unknown) => void>>();
	private _status: Status = 'idle';

	constructor(options: RealtimeOptions) {
		this.opts = {
			protocols: [SUBPROTOCOL_MSGPACK, SUBPROTOCOL_JSON],
			heartbeatIntervalMs: 15_000,
			heartbeatMisses: 3,
			backoff: { initialMs: 500, maxMs: 30_000 },
			WebSocket: globalThis.WebSocket,
			...options
		};
	}

	get status(): Status {
		return this._status;
	}

	/** Subscribes to status changes; returns an unsubscribe function. */
	onStatus(fn: (s: Status) => void): () => void {
		this.listeners.add(fn);
		fn(this._status);
		return () => this.listeners.delete(fn);
	}

	/**
	 * Handles notifications of a method (other than heartbeats and stream
	 * events of subscriptions made with subscribe()); returns a function
	 * removing the handler. "stream.closed" for subscriptions not made with
	 * subscribe() (e.g. planner.watch) arrives here too.
	 */
	on(method: string, fn: (params: unknown) => void): () => void {
		let set = this.handlers.get(method);
		if (!set) this.handlers.set(method, (set = new Set()));
		set.add(fn);
		return () => set.delete(fn);
	}

	/** Starts connecting; the client keeps the connection up until close(). */
	connect(): void {
		if (!this.stopped) return;
		this.stopped = false;
		this.open();
	}

	/** Closes the connection and stops reconnecting. */
	close(): void {
		this.stopped = true;
		clearTimeout(this.reconnectTimer);
		this.teardown(new Error('client closed'));
		this.ws?.close(1000, 'client closed');
		this.ws = undefined;
		this.setStatus('closed');
	}

	/** Sends a request once the connection is open and resolves with the result. */
	async call<T = unknown>(method: string, params?: unknown): Promise<T> {
		await this.ready();
		return this.request<T>(method, params);
	}

	/** Subscribes to a stream (e.g. "project:WEB"). */
	subscribe(stream: string, handlers: StreamHandlers): Subscription {
		const sub = new Subscription(this, stream, handlers);
		this.subs.add(sub);
		if (this._status === 'open') void this.sendSubscribe(sub);
		return sub;
	}

	/** @internal */
	removeSubscription(sub: Subscription): void {
		sub.closed = true;
		this.subs.delete(sub);
		if (sub.serverId) {
			this.byServerId.delete(sub.serverId);
			if (this._status === 'open') {
				this.request('stream.unsubscribe', { subscription: sub.serverId }).catch(() => {});
			}
		}
	}

	private setStatus(s: Status): void {
		this._status = s;
		for (const fn of this.listeners) fn(s);
	}

	private ready(): Promise<void> {
		if (this._status === 'open') return Promise.resolve();
		if (this.stopped) return Promise.reject(new Error('client is not connected'));
		return new Promise((resolve) => this.waiting.push(resolve));
	}

	private open(): void {
		this.setStatus(this.attempt === 0 ? 'connecting' : 'reconnecting');
		const ws = new this.opts.WebSocket(this.opts.url, this.opts.protocols);
		ws.binaryType = 'arraybuffer';
		this.ws = ws;
		ws.onopen = () => void this.onOpen(ws);
		ws.onmessage = (ev) => this.onMessage(ws, ev.data);
		ws.onclose = () => this.onClose(ws);
		ws.onerror = () => {}; // followed by onclose
	}

	private async onOpen(ws: WebSocket): Promise<void> {
		try {
			this.codec = codecFor(ws.protocol);
		} catch {
			ws.close(1002, 'no supported subprotocol');
			return;
		}
		this.lastSeen = Date.now();
		this.startHeartbeat(ws);
		const token = await this.opts.getToken();
		if (!token) {
			this.stopped = true;
			ws.close(1000, 'no token');
			this.setStatus('unauthenticated');
			return;
		}
		try {
			const res = await this.request<{ subject: string; expires_at?: number }>('auth', { token });
			this.scheduleRefresh(res.expires_at);
		} catch {
			ws.close(4001, 'auth failed');
			return;
		}
		this.attempt = 0;
		this.setStatus('open');
		for (const sub of this.subs) void this.sendSubscribe(sub);
		const waiting = this.waiting;
		this.waiting = [];
		for (const resolve of waiting) resolve();
	}

	private onMessage(ws: WebSocket, data: unknown): void {
		if (ws !== this.ws || !this.codec) return;
		this.lastSeen = Date.now();
		let m: Envelope;
		try {
			m = this.codec.decode(data);
		} catch {
			return;
		}
		if (m.method === undefined && m.id !== undefined) {
			const p = this.pending.get(m.id);
			if (!p) return;
			this.pending.delete(m.id);
			if (m.error) p.reject(new RpcError(m.error.code, m.error.message));
			else p.resolve(m.result);
			return;
		}
		if (m.method === undefined) return;
		if (m.id !== undefined) {
			// The client exposes no methods to the server.
			this.send({
				jsonrpc: '2.0',
				id: m.id,
				error: { code: -32601, message: `method ${m.method} not found` }
			});
			return;
		}
		switch (m.method) {
			case '$/heartbeat':
				return;
			case 'stream.event':
				return this.onStreamEvent(m.params as StreamEvent);
			case 'stream.closed': {
				const sub = this.byServerId.get((m.params as { subscription: string }).subscription);
				if (sub) {
					this.byServerId.delete(sub.serverId!);
					void this.sendSubscribe(sub);
					return;
				}
				this.notify(m.method, m.params);
				return;
			}
			default:
				this.notify(m.method, m.params);
		}
	}

	private notify(method: string, params: unknown): void {
		this.opts.onNotification?.(method, params);
		for (const fn of this.handlers.get(method) ?? []) fn(params);
	}

	private onStreamEvent(e: StreamEvent): void {
		const sub = this.byServerId.get(e.subscription);
		if (!sub || sub.closed) return;
		if (sub.lastSeq !== undefined && e.seq <= sub.lastSeq) return; // already delivered
		sub.lastSeq = e.seq;
		sub.handlers.onEvent(e);
	}

	private async sendSubscribe(sub: Subscription): Promise<void> {
		// The client chooses the subscription ID: replayed events may arrive
		// before the response and must already be routable.
		const id = `c${++this.nextSubId}`;
		const params: { subscription: string; stream: string; from_seq?: number } = {
			subscription: id,
			stream: sub.stream
		};
		if (sub.lastSeq !== undefined) params.from_seq = sub.lastSeq;
		sub.serverId = id;
		this.byServerId.set(id, sub);
		try {
			const res = await this.request<{ subscription: string; seq: number }>(
				'stream.subscribe',
				params
			);
			if (sub.closed) {
				this.request('stream.unsubscribe', { subscription: id }).catch(() => {});
				return;
			}
			if (sub.lastSeq === undefined) sub.lastSeq = res.seq;
		} catch (err) {
			this.byServerId.delete(id);
			if (sub.serverId === id) sub.serverId = undefined;
			if (err instanceof RpcError && err.code === CODE_RESYNC_REQUIRED && !sub.closed) {
				sub.lastSeq = undefined;
				sub.handlers.onResync?.();
				await this.sendSubscribe(sub);
			}
		}
	}

	private request<T>(method: string, params?: unknown): Promise<T> {
		const id = ++this.nextId;
		return new Promise<T>((resolve, reject) => {
			this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
			if (!this.send({ jsonrpc: '2.0', id, method, params: params ?? {} })) {
				this.pending.delete(id);
				reject(new Error('connection closed'));
			}
		});
	}

	private send(m: Envelope): boolean {
		const ws = this.ws;
		if (!ws || ws.readyState !== ws.OPEN || !this.codec) return false;
		ws.send(this.codec.encode(m));
		return true;
	}

	private startHeartbeat(ws: WebSocket): void {
		clearInterval(this.heartbeatTimer);
		const interval = this.opts.heartbeatIntervalMs;
		this.heartbeatTimer = setInterval(() => {
			if (ws !== this.ws) return;
			if (Date.now() - this.lastSeen > interval * this.opts.heartbeatMisses) {
				ws.close(4000, 'heartbeat timeout');
				return;
			}
			this.send({ jsonrpc: '2.0', method: '$/heartbeat', params: { time: Date.now() } });
		}, interval);
	}

	private scheduleRefresh(expiresAt: number | undefined): void {
		clearTimeout(this.refreshTimer);
		if (!expiresAt) return;
		const wait = Math.max(0, (expiresAt * 1000 - Date.now()) * 0.8);
		this.refreshTimer = setTimeout(async () => {
			const token = await this.opts.getToken();
			if (!token) return;
			try {
				const res = await this.request<{ expires_at?: number }>('auth.refresh', { token });
				this.scheduleRefresh(res.expires_at);
			} catch {
				this.scheduleRefresh(Date.now() / 1000 + 1.25); // retry in ~1 s
			}
		}, wait);
	}

	private onClose(ws: WebSocket): void {
		if (ws !== this.ws) return;
		this.teardown(new Error('connection closed'));
		this.ws = undefined;
		if (this.stopped) return;
		this.attempt++;
		const { initialMs, maxMs } = this.opts.backoff;
		const base = Math.min(maxMs, initialMs * 2 ** (this.attempt - 1));
		const delay = base / 2 + Math.random() * (base / 2);
		this.setStatus('reconnecting');
		this.reconnectTimer = setTimeout(() => this.open(), delay);
	}

	private teardown(reason: Error): void {
		clearInterval(this.heartbeatTimer);
		clearTimeout(this.refreshTimer);
		for (const p of this.pending.values()) p.reject(reason);
		this.pending.clear();
		this.byServerId.clear();
		for (const sub of this.subs) sub.serverId = undefined;
	}
}

/** Svelte store of the client's connection status. */
export function statusStore(client: RealtimeClient): Readable<Status> {
	return readable<Status>(client.status, (set) => client.onStatus(set));
}
