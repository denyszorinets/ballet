<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	const key = $derived(page.params.customer ?? '');

	let session = $state<Session>();
	let customer = $state<Schemas['Customer']>();
	let projects = $state<Schemas['Project'][]>([]);
	let error = $state<string>();

	let renaming = $state(false);
	let newName = $state('');
	let renameError = $state<string>();

	let form = $state({ key: '', name: '', description: '' });
	let formError = $state<string>();

	async function load(s: Session, k: string) {
		const [c, p] = await Promise.all([
			s.api.GET('/api/v1/customers/{customer}', { params: { path: { customer: k } } }),
			s.api.GET('/api/v1/customers/{customer}/projects', { params: { path: { customer: k } } })
		]);
		if (!c.data) {
			error = apiError(c.error);
			return;
		}
		customer = c.data;
		projects = p.data?.items ?? [];
	}

	$effect(() => {
		const k = key;
		getSession()
			.then((s) => {
				session = s;
				return load(s, k);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function rename(e: SubmitEvent) {
		e.preventDefault();
		if (!session || !customer) return;
		const { data, error: err } = await session.api.PATCH('/api/v1/customers/{customer}', {
			params: { path: { customer: key } },
			body: { name: newName, version: customer.version }
		});
		if (err) {
			renameError = apiError(err);
			return;
		}
		customer = data;
		renaming = false;
	}

	async function createProject(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		formError = undefined;
		const { error: err } = await session.api.POST('/api/v1/customers/{customer}/projects', {
			params: { path: { customer: key } },
			body: form
		});
		if (err) {
			formError = apiError(err);
			return;
		}
		form = { key: '', name: '', description: '' };
		await load(session, key);
	}
</script>

<svelte:head><title>{customer?.name ?? key} · Ballet</title></svelte:head>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !customer}
	<p class="muted">Loading…</p>
{:else}
	<div class="title">
		{#if renaming}
			<form class="form" onsubmit={rename} aria-label="Rename customer">
				<label>Name <input bind:value={newName} required /></label>
				<button class="primary" type="submit">Save</button>
				<button type="button" onclick={() => (renaming = false)}>Cancel</button>
				{#if renameError}<p class="error" role="alert">{renameError}</p>{/if}
			</form>
		{:else}
			<h1>{customer.name}</h1>
			<span class="mono muted">{customer.key}</span>
			{#if session?.permissions.can('customer.update', { customer: key })}
				<button
					onclick={() => {
						newName = customer?.name ?? '';
						renameError = undefined;
						renaming = true;
					}}>Rename</button
				>
			{/if}
		{/if}
	</div>

	{#if session?.permissions.can('knowledge.read', { customer: key })}
		<p><a href={resolve('/customers/[customer]/knowledge', { customer: key })}>Knowledge</a></p>
	{/if}

	<h2>Projects</h2>
	{#if projects.length === 0}
		<p class="muted">No projects yet.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<thead><tr><th>Key</th><th>Name</th><th>Description</th></tr></thead>
				<tbody>
					{#each projects as p (p.id)}
						<tr>
							<td class="mono"
								><a href={resolve('/projects/[project]', { project: p.key })}>{p.key}</a></td
							>
							<td>{p.name}</td>
							<td class="muted">{p.description}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}

	{#if session?.permissions.can('project.create', { customer: key })}
		<h2>New project</h2>
		<form class="form card" onsubmit={createProject} aria-label="New project">
			<label
				>Key <input
					bind:value={form.key}
					required
					placeholder="WEB"
					pattern="[A-Z][A-Z0-9]+"
				/></label
			>
			<label>Name <input bind:value={form.name} required /></label>
			<label class="grow">Description <input bind:value={form.description} /></label>
			<button class="primary" type="submit">Create project</button>
			{#if formError}<p class="error" role="alert">{formError}</p>{/if}
		</form>
	{/if}
{/if}

<style>
	.title {
		display: flex;
		align-items: baseline;
		gap: 1rem;
		flex-wrap: wrap;
	}
	.title h1 {
		margin: 0;
	}
	.grow {
		flex: 1;
		min-width: 12rem;
	}
</style>
