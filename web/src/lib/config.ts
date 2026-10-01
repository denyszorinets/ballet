/** Runtime configuration served by Core at /config.json. */
export interface AppConfig {
	oidc: { issuer: string; client_id: string };
}

export async function loadConfig(fetchFn: typeof fetch = fetch): Promise<AppConfig> {
	const res = await fetchFn('/config.json');
	if (!res.ok) throw new Error(`loading /config.json failed: ${res.status}`);
	return (await res.json()) as AppConfig;
}
