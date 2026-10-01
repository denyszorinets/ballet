export interface Health {
	service: string;
	status: 'ok' | 'unreachable';
}

/** Asks Core whether it is alive. Never throws: failures map to "unreachable". */
export async function fetchHealth(fetchFn: typeof fetch = fetch): Promise<Health> {
	try {
		const res = await fetchFn('/healthz');
		if (!res.ok) return { service: 'core', status: 'unreachable' };
		return (await res.json()) as Health;
	} catch {
		return { service: 'core', status: 'unreachable' };
	}
}
