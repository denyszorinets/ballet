<script lang="ts">
	import WorkControl from '$lib/components/WorkControl.svelte';
	import { resolve } from '$app/paths';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	let session = $state<Session>();
	let organizations = $state<Schemas['Organization'][]>();
	let error = $state<string>();
	let form = $state({ key: '', name: '' });
	let formError = $state<string>();
	let saving = $state(false);

	async function load(s: Session) {
		const { data, error: err } = await s.api.GET('/api/v1/organizations');
		if (data) organizations = data.items;
		else error = apiError(err);
	}

	$effect(() => {
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		saving = true;
		formError = undefined;
		const { error: err } = await session.api.POST('/api/v1/organizations', { body: form });
		saving = false;
		if (err) {
			formError = apiError(err);
			return;
		}
		form = { key: '', name: '' };
		await load(session);
	}
</script>

<svelte:head><title>Organizations · Ballet</title></svelte:head>

<h1>Organizations</h1>
{#if session?.permissions.can('run.manage')}
	<WorkControl />
{/if}

{#if session?.permissions.can('organization.create')}
	<form class="form card" onsubmit={create} aria-label="New organization">
		<label
			>Key <input
				bind:value={form.key}
				required
				placeholder="acme"
				pattern="[a-z][a-z0-9\-]+"
			/></label
		>
		<label>Name <input bind:value={form.name} required placeholder="Acme Corporation" /></label>
		<button class="primary" type="submit" disabled={saving}>Create organization</button>
		{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	</form>
{/if}

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !organizations}
	<p class="muted">Loading…</p>
{:else if organizations.length === 0}
	<p class="muted">There are no organizations you can see yet.</p>
{:else}
	<ul class="list">
		{#each organizations as c (c.id)}
			<li>
				<a
					class="card row"
					href={resolve('/organizations/[organization]', { organization: c.key })}
				>
					<span class="mono muted key">{c.key}</span>
					<span>{c.name}</span>
				</a>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.form {
		margin-bottom: 1rem;
	}
	.list {
		list-style: none;
		padding: 0;
		display: grid;
		gap: 0.5rem;
	}
	.row {
		display: flex;
		gap: 1rem;
		align-items: baseline;
		color: var(--text);
		text-decoration: none;
	}
	.row:hover {
		border-color: var(--accent);
	}
	.key {
		min-width: 8rem;
	}
</style>
