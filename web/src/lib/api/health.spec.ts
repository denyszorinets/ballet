import { describe, expect, it } from 'vitest';
import { fetchHealth } from './health';

function fakeFetch(status: number, body: unknown): typeof fetch {
	return async () => new Response(JSON.stringify(body), { status });
}

describe('fetchHealth', () => {
	it('returns the reported status when the service is healthy', async () => {
		const health = await fetchHealth(fakeFetch(200, { service: 'core', status: 'ok' }));
		expect(health).toEqual({ service: 'core', status: 'ok' });
	});

	it('reports the service as unreachable on a non-2xx response', async () => {
		const health = await fetchHealth(fakeFetch(502, {}));
		expect(health).toEqual({ service: 'core', status: 'unreachable' });
	});

	it('reports the service as unreachable when the request fails', async () => {
		const failing: typeof fetch = async () => {
			throw new TypeError('network down');
		};
		expect(await fetchHealth(failing)).toEqual({ service: 'core', status: 'unreachable' });
	});
});
