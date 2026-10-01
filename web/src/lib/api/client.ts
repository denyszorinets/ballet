import createClient, { type Middleware } from 'openapi-fetch';
import type { components, paths } from './schema';

export type Schemas = components['schemas'];
export type ApiError = Schemas['Error'];

export interface ClientOptions {
	/** Returns the current access token, or undefined when logged out. */
	getToken: () => Promise<string | undefined>;
	/** Base URL of Core; defaults to the current origin. */
	baseUrl?: string;
	fetch?: typeof fetch;
}

/**
 * Typed client for Core's REST API, generated from core/api/openapi.yaml
 * (run `bun run generate:api` after changing the spec).
 */
export function createApiClient(opts: ClientOptions) {
	const client = createClient<paths>({
		baseUrl:
			opts.baseUrl ?? (typeof window === 'undefined' ? 'http://localhost' : window.location.origin),
		fetch: opts.fetch
	});
	const auth: Middleware = {
		async onRequest({ request }) {
			const token = await opts.getToken();
			if (token) request.headers.set('Authorization', `Bearer ${token}`);
			return request;
		}
	};
	client.use(auth);
	return client;
}

export type ApiClient = ReturnType<typeof createApiClient>;

/** Human-readable message of an API error response. */
export function apiError(err: ApiError | undefined): string {
	return err?.message ?? 'Unexpected error';
}
