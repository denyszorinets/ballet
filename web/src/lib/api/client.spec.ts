import { describe, expect, it } from 'vitest';
import { apiError, createApiClient } from './client';

function recordingFetch(status: number, body: unknown) {
	const calls: Request[] = [];
	const fetchFn = async (input: Request) => {
		calls.push(input);
		return new Response(JSON.stringify(body), {
			status,
			headers: { 'Content-Type': 'application/json' }
		});
	};
	return { calls, fetchFn: fetchFn as unknown as typeof fetch };
}

describe('createApiClient', () => {
	it('sends the bearer token and returns typed data', async () => {
		const { calls, fetchFn } = recordingFetch(200, { items: [] });
		const api = createApiClient({ getToken: async () => 'tok-123', fetch: fetchFn });

		const { data } = await api.GET('/api/v1/organizations');

		expect(data).toEqual({ items: [] });
		expect(calls[0].headers.get('Authorization')).toBe('Bearer tok-123');
		expect(new URL(calls[0].url).pathname).toBe('/api/v1/organizations');
	});

	it('omits the Authorization header without a token', async () => {
		const { calls, fetchFn } = recordingFetch(401, {
			error: 'unauthenticated',
			message: 'a valid bearer token is required'
		});
		const api = createApiClient({ getToken: async () => undefined, fetch: fetchFn });

		const { error } = await api.GET('/api/v1/me');

		expect(calls[0].headers.get('Authorization')).toBeNull();
		expect(error?.error).toBe('unauthenticated');
	});

	it('fills path parameters', async () => {
		const { calls, fetchFn } = recordingFetch(200, {});
		const api = createApiClient({ getToken: async () => 't', fetch: fetchFn });

		await api.GET('/api/v1/items/{item}', { params: { path: { item: 'WEB-42' } } });

		expect(new URL(calls[0].url).pathname).toBe('/api/v1/items/WEB-42');
	});
});

describe('apiError', () => {
	it('formats API errors for display', () => {
		expect(apiError({ error: 'conflict', message: 'stale version' })).toBe('stale version');
		expect(apiError(undefined)).toBe('Unexpected error');
	});
});
