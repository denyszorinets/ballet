import { createApiClient, type ApiClient } from '$lib/api/client';
import { Auth } from '$lib/auth/auth.svelte';
import { loadConfig } from '$lib/config';
import { Permissions } from '$lib/permissions.svelte';
import { RealtimeClient } from '$lib/realtime/client';

/** The app's shared clients, created once at startup. */
export interface Session {
	auth: Auth;
	api: ApiClient;
	realtime: RealtimeClient;
	permissions: Permissions;
}

let session: Promise<Session> | undefined;

/** Returns the session, creating it on first use. */
export function getSession(): Promise<Session> {
	session ??= create();
	return session;
}

async function create(): Promise<Session> {
	const config = await loadConfig();
	const origin = window.location.origin;
	const auth = new Auth(config, origin);
	await auth.init();
	const getToken = () => auth.getToken();
	const wsOrigin = origin.replace(/^http/, 'ws');
	const api = createApiClient({ getToken });
	const permissions = new Permissions();
	if (auth.authenticated) await permissions.load(api).catch(() => {});
	return {
		auth,
		api,
		realtime: new RealtimeClient({ url: `${wsOrigin}/rpc`, getToken }),
		permissions
	};
}
