// Integration tests against the Go reference implementation
// (kit/rpc/cmd/rpc-testserver). Requires the Go toolchain.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { RealtimeClient, type Status, type StreamEvent } from './client';
import { SUBPROTOCOL_JSON, SUBPROTOCOL_MSGPACK } from './codec';

const bin = path.join(mkdtempSync(path.join(tmpdir(), 'rpc-')), 'rpc-testserver');
const servers: ChildProcess[] = [];
const clients: RealtimeClient[] = [];

beforeAll(() => {
	execFileSync('go', ['build', '-o', bin, './rpc/cmd/rpc-testserver'], {
		cwd: path.resolve(import.meta.dirname, '../../../../kit'),
		stdio: 'inherit'
	});
}, 120_000);

afterEach(() => {
	for (const c of clients.splice(0)) c.close();
	for (const s of servers.splice(0)) s.kill();
});

async function startServer(...args: string[]): Promise<string> {
	const proc = spawn(bin, args);
	servers.push(proc);
	const addr = await new Promise<string>((resolve, reject) => {
		proc.stdout!.on('data', (d: Buffer) => {
			const m = /listening (\S+)/.exec(d.toString());
			if (m) resolve(m[1]);
		});
		proc.on('exit', (code) => reject(new Error(`test server exited with ${code}`)));
	});
	return `ws://${addr}/rpc`;
}

function client(url: string, opts: Partial<ConstructorParameters<typeof RealtimeClient>[0]> = {}) {
	const c = new RealtimeClient({
		url,
		getToken: async () => 'valid:alice',
		heartbeatIntervalMs: 200,
		backoff: { initialMs: 20, maxMs: 100 },
		...opts
	});
	clients.push(c);
	return c;
}

async function waitFor(cond: () => boolean, timeoutMs = 5000): Promise<void> {
	const start = Date.now();
	while (!cond()) {
		if (Date.now() - start > timeoutMs) throw new Error('timed out waiting for condition');
		await new Promise((r) => setTimeout(r, 10));
	}
}

describe('RealtimeClient against the Go server', () => {
	for (const protocol of [SUBPROTOCOL_MSGPACK, SUBPROTOCOL_JSON]) {
		it(`calls methods over ${protocol}`, async () => {
			const c = client(await startServer(), { protocols: [protocol] });
			c.connect();

			expect(await c.call('echo', { text: 'hi', n: 3, tags: ['a'] })).toEqual({
				text: 'hi',
				n: 3,
				tags: ['a']
			});
			expect(await c.call('whoami')).toBe('alice');
			await expect(c.call('nope')).rejects.toMatchObject({ code: -32601 });
		});
	}

	it('delivers notifications in order', async () => {
		const got: number[] = [];
		const c = client(await startServer(), {
			onNotification: (method, params) => {
				if (method === 'test.event') got.push((params as { i: number }).i);
			}
		});
		c.connect();

		await c.call('test.emit', { count: 200 });

		await waitFor(() => got.length === 200);
		expect(got).toEqual([...Array(200).keys()]);
	});

	it('dispatches notifications to handlers added with on()', async () => {
		const c = client(await startServer());
		const got: number[] = [];
		const off = c.on('test.event', (params) => got.push((params as { i: number }).i));
		c.connect();
		await c.call('test.emit', { count: 3 });
		await waitFor(() => got.length === 3);
		off();
		await c.call('test.emit', { count: 2 });
		await c.call('echo', {});
		expect(got).toEqual([0, 1, 2]);
	});

	it('reconnects and resumes subscriptions without gaps or duplicates', async () => {
		const url = await startServer();
		const writer = client(url);
		writer.connect();
		const c = client(url);
		const statuses: Status[] = [];
		c.onStatus((s) => statuses.push(s));
		c.connect();
		const keys: string[] = [];
		c.subscribe('project:T', { onEvent: (e: StreamEvent) => keys.push(e.entity_key!) });
		await waitFor(() => c.status === 'open');
		await new Promise((r) => setTimeout(r, 50)); // subscription established

		await writer.call('test.append', { count: 2 });
		await waitFor(() => keys.length === 2);

		await c.call('test.close'); // server drops this connection
		await waitFor(() => c.status === 'reconnecting');
		await writer.call('test.append', { count: 3 }); // missed while disconnected
		await waitFor(() => c.status === 'open');
		await writer.call('test.append', { count: 1 });

		await waitFor(() => keys.length === 6);
		expect(keys).toEqual(['T-1', 'T-2', 'T-3', 'T-4', 'T-5', 'T-6']);
		expect(statuses).toContain('reconnecting');
	});

	it('asks for a resync when too much was missed', async () => {
		const url = await startServer('-max-replay', '2');
		const writer = client(url);
		writer.connect();
		const c = client(url);
		c.connect();
		const keys: string[] = [];
		let resyncs = 0;
		c.subscribe('project:T', {
			onEvent: (e) => keys.push(e.entity_key!),
			onResync: () => resyncs++
		});
		await waitFor(() => c.status === 'open');
		await new Promise((r) => setTimeout(r, 50));
		await writer.call('test.append', { count: 1 });
		await waitFor(() => keys.length === 1);

		await c.call('test.close');
		await waitFor(() => c.status === 'reconnecting');
		await writer.call('test.append', { count: 5 });
		await waitFor(() => resyncs === 1);
		await new Promise((r) => setTimeout(r, 50));
		await writer.call('test.append', { count: 1 });

		await waitFor(() => keys.includes('T-7'));
		expect(keys).toEqual(['T-1', 'T-7']);
	});

	it('refreshes short-lived tokens and stays connected', async () => {
		let issued = 0;
		const c = client(await startServer('-short-ttl', '1s'), {
			getToken: async () => {
				issued++;
				return 'short:alice';
			}
		});
		c.connect();
		await waitFor(() => c.status === 'open');

		await new Promise((r) => setTimeout(r, 2500));

		expect(await c.call('whoami')).toBe('alice');
		expect(issued).toBeGreaterThanOrEqual(3);
	}, 10_000);

	it('reconnects when the server falls silent', async () => {
		// The server heartbeats every 2 s; the client expects one every 50 ms.
		const c = client(await startServer('-heartbeat', '2s'), {
			heartbeatIntervalMs: 50,
			heartbeatMisses: 2
		});
		const statuses: Status[] = [];
		c.onStatus((s) => statuses.push(s));
		c.connect();

		await waitFor(() => statuses.includes('reconnecting'));
		expect(statuses[0]).toBe('idle');
	});

	it('stops when there is no token and retries on rejected tokens', async () => {
		const url = await startServer();
		const loggedOut = client(url, { getToken: async () => undefined });
		loggedOut.connect();
		await waitFor(() => loggedOut.status === 'unauthenticated');

		const forged = client(url, { getToken: async () => 'forged' });
		forged.connect();
		await waitFor(() => forged.status === 'reconnecting');

		expect(loggedOut.status).toBe('unauthenticated');
		expect(forged.status).toBe('reconnecting');
	});
});
