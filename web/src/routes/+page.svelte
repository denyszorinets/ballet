<script lang="ts">
	import { resolve } from '$app/paths';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	let session = $state<Session>();
	let customers = $state<Schemas['Customer'][]>();
	let error = $state<string>();
	let form = $state({ key: '', name: '' });
	let formError = $state<string>();
	let saving = $state(false);

	async function load(s: Session) {
		const { data, error: err } = await s.api.GET('/api/v1/customers');
		if (data) customers = data.items;
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
		const { error: err } = await session.api.POST('/api/v1/customers', { body: form });
		saving = false;
		if (err) {
			formError = apiError(err);
			return;
		}
		form = { key: '', name: '' };
		await load(session);
	}
</script>

<svelte:head><title>Customers · Ballet</title></svelte:head>

<h1>Customers</h1>

{#if session?.permissions.can('customer.create')}
	<form class="form card" onsubmit={create} aria-label="New customer">
		<label
			>Key <input
				bind:value={form.key}
				required
				placeholder="acme"
				pattern="[a-z][a-z0-9\-]+"
			/></label
		>
		<label>Name <input bind:value={form.name} required placeholder="Acme Corporation" /></label>
		<button class="primary" type="submit" disabled={saving}>Create customer</button>
		{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	</form>
{/if}

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !customers}
	<p class="muted">Loading…</p>
{:else if customers.length === 0}
	<p class="muted">There are no customers you can see yet.</p>
{:else}
	<ul class="list">
		{#each customers as c (c.id)}
			<li>
				<a class="card row" href={resolve('/customers/[customer]', { customer: c.key })}>
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
