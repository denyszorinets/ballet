<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession } from '$lib/session';

	let customers = $state<Schemas['Customer'][]>();
	let error = $state<string>();

	$effect(() => {
		getSession()
			.then((s) => s.api.GET('/api/v1/customers'))
			.then(({ data, error: err }) => {
				if (data) customers = data.items;
				else error = apiError(err);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});
</script>

<svelte:head><title>Customers · Ballet</title></svelte:head>

<h1>Customers</h1>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !customers}
	<p class="muted">Loading…</p>
{:else if customers.length === 0}
	<p class="muted">There are no customers you can see yet.</p>
{:else}
	<ul class="list">
		{#each customers as c (c.id)}
			<li class="card">
				<span class="key">{c.key}</span>
				<span>{c.name}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.list {
		list-style: none;
		padding: 0;
		display: grid;
		gap: 0.5rem;
	}
	.list li {
		display: flex;
		gap: 1rem;
		align-items: baseline;
	}
	.key {
		font-family: ui-monospace, monospace;
		color: var(--muted);
		min-width: 8rem;
	}
</style>
