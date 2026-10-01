import type { Page } from '@playwright/test';

/** Handles one JSON-RPC request; notify pushes a notification to the page. */
export type RpcHandler = (
	method: string,
	params: Record<string, unknown>,
	notify: (method: string, params: unknown) => Promise<void>
) => Promise<unknown> | unknown;

/**
 * Replaces the page's WebSocket with an in-page fake speaking the JSON
 * subprotocol of Core's realtime API (/rpc). Requests go to handler in the
 * test (Node); auth succeeds for any token; heartbeats are answered.
 */
export async function fakeRealtime(page: Page, handler: RpcHandler): Promise<void> {
	const notify = async (method: string, params: unknown) => {
		const text = JSON.stringify({ jsonrpc: '2.0', method, params });
		await page.evaluate(
			(t) => (window as unknown as { __rpcPush(t: string): void }).__rpcPush(t),
			text
		);
	};
	await page.exposeFunction('__rpcSend', async (text: string): Promise<string[]> => {
		const m = JSON.parse(text) as { id?: number; method: string; params?: Record<string, unknown> };
		if (m.method === '$/heartbeat') return [];
		const reply = (body: object) => JSON.stringify({ jsonrpc: '2.0', id: m.id, ...body });
		if (m.method === 'auth' || m.method === 'auth.refresh') {
			return [
				reply({
					result: { subject: 'user-alice', expires_at: Math.floor(Date.now() / 1000) + 3600 }
				})
			];
		}
		try {
			const result = await handler(m.method, m.params ?? {}, notify);
			return m.id === undefined ? [] : [reply({ result: result ?? {} })];
		} catch (e) {
			const err = e as { code?: number; message?: string };
			return [reply({ error: { code: err.code ?? -32603, message: err.message ?? String(e) } })];
		}
	});
	await page.addInitScript(() => {
		const sockets: FakeSocket[] = [];
		class FakeSocket extends EventTarget {
			static CONNECTING = 0;
			static OPEN = 1;
			static CLOSING = 2;
			static CLOSED = 3;
			readonly CONNECTING = 0;
			readonly OPEN = 1;
			readonly CLOSING = 2;
			readonly CLOSED = 3;
			readyState = 0;
			protocol = 'ballet.v1.json';
			binaryType = 'blob';
			onopen: ((e: Event) => void) | null = null;
			onmessage: ((e: MessageEvent) => void) | null = null;
			onclose: ((e: CloseEvent) => void) | null = null;
			onerror: ((e: Event) => void) | null = null;
			constructor(readonly url: string) {
				super();
				sockets.push(this);
				setTimeout(() => {
					this.readyState = 1;
					this.onopen?.(new Event('open'));
				}, 0);
			}
			send(data: string) {
				const send = (window as unknown as { __rpcSend(t: string): Promise<string[]> }).__rpcSend;
				void send(data).then((replies) => replies.forEach((r) => this.deliver(r)));
			}
			deliver(text: string) {
				if (this.readyState === 1) this.onmessage?.(new MessageEvent('message', { data: text }));
			}
			close(code = 1000, reason = '') {
				if (this.readyState === 3) return;
				this.readyState = 3;
				this.onclose?.(new CloseEvent('close', { code, reason }));
			}
		}
		(window as unknown as { __rpcPush(t: string): void }).__rpcPush = (text: string) =>
			sockets.forEach((s) => s.deliver(text));
		(window as unknown as { WebSocket: unknown }).WebSocket = FakeSocket;
	});
}
