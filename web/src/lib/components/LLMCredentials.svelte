<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	let { organization }: { organization: string } = $props();

	type Provider = 'anthropic' | 'openai';

	let session = $state<Session>();
	let items = $state<Schemas['Credential'][]>([]);
	let provider = $state<Provider>('anthropic');
	let apiKey = $state('');
	let baseURL = $state('');
	let error = $state<string>();
	let message = $state<string>();

	async function load(s: Session) {
		const { data, error: err } = await s.api.GET(
			'/api/v1/organizations/{organization}/credentials',
			{
				params: { path: { organization } }
			}
		);
		if (!data) {
			error = apiError(err);
			return;
		}
		items = data.items.filter((c) => c.provider !== 'git');
	}

	$effect(() => {
		void organization;
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		error = message = undefined;
		const { error: err, response } = await session.api.PUT(
			'/api/v1/organizations/{organization}/credentials/{provider}',
			{
				params: { path: { organization, provider } },
				body: { api_key: apiKey, ...(baseURL ? { base_url: baseURL } : {}) }
			}
		);
		if (!response.ok) {
			error = apiError(err);
			return;
		}
		apiKey = baseURL = '';
		message = `Saved the ${provider} key.`;
		await load(session);
	}

	async function remove(p: Provider) {
		if (!session || !confirm(`Remove the ${p} key of ${organization}?`)) return;
		const { error: err, response } = await session.api.DELETE(
			'/api/v1/organizations/{organization}/credentials/{provider}',
			{ params: { path: { organization, provider: p } } }
		);
		if (!response.ok) {
			error = apiError(err);
			return;
		}
		await load(session);
	}
</script>

<section aria-label="LLM credentials">
	<h2>LLM credentials</h2>
	<p class="muted">
		Keys the LLM gateway uses for this organization's agent sessions and planner. They are stored
		encrypted and never shown again; projects may override them.
	</p>
	{#if items.length}
		<ul class="list">
			{#each items as c (c.provider + (c.project ?? ''))}
				<li>
					<strong>{c.provider}</strong>
					{#if c.project}<span class="muted">(project {c.project})</span>{/if}
					<span class="mono muted small">{c.fingerprint}</span>
					{#if c.base_url}<span class="muted small">{c.base_url}</span>{/if}
					{#if !c.project}<button onclick={() => remove(c.provider as Provider)}>Remove</button
						>{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="muted">No key yet: agent sessions cannot call a model.</p>
	{/if}
	<form class="form card" onsubmit={save} aria-label="Set a key">
		<label
			>Provider
			<select bind:value={provider}>
				<option value="anthropic">Anthropic</option>
				<option value="openai">OpenAI</option>
			</select>
		</label>
		<label class="grow">API key <input type="password" bind:value={apiKey} required /></label>
		<label class="grow"
			>Base URL <input bind:value={baseURL} placeholder="default of the provider" /></label
		>
		<button class="primary" type="submit">Save key</button>
	</form>
	{#if message}<p role="status">{message}</p>{/if}
	{#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
	.list {
		list-style: none;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.list li {
		display: flex;
		gap: 0.5rem;
		align-items: center;
		flex-wrap: wrap;
	}
	.small {
		font-size: 0.85rem;
	}
	.grow {
		flex: 1;
		min-width: 12rem;
	}
</style>
