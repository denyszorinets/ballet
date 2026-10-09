<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError } from '$lib/api/client';
	import { KINDS, kindLabel, type KnowledgeEntry, type KnowledgeKind } from '$lib/knowledge';
	import { getSession, type Session } from '$lib/session';

	const organization = $derived(page.params.organization ?? '');

	let session = $state<Session>();
	let entries = $state<KnowledgeEntry[]>([]);
	let loaded = $state(false);
	let error = $state<string>();

	let query = $state('');
	let kind = $state<KnowledgeKind | ''>('');
	let project = $state('');
	/** The query the shown entries are results for; empty = plain listing. */
	let searched = $state('');

	async function load(s: Session) {
		error = undefined;
		const filters = { kind: kind || undefined, project: project.trim() || undefined };
		const q = query.trim();
		const res = q
			? await s.api.GET('/api/v1/organizations/{organization}/knowledge/search', {
					params: { path: { organization }, query: { q, ...filters } }
				})
			: await s.api.GET('/api/v1/organizations/{organization}/knowledge/entries', {
					params: { path: { organization }, query: filters }
				});
		if (res.error) {
			error = apiError(res.error);
			return;
		}
		entries = q
			? (res.data as { items: { entry: KnowledgeEntry }[] }).items.map((h) => h.entry)
			: (res.data as { items: KnowledgeEntry[] }).items;
		searched = q;
		loaded = true;
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

	function submit(e: SubmitEvent) {
		e.preventDefault();
		if (session) void load(session);
	}
</script>

<svelte:head><title>Knowledge · {organization} · Ballet</title></svelte:head>

<div class="title">
	<h1>Knowledge</h1>
	<a class="mono muted" href={resolve('/organizations/[organization]', { organization })}
		>{organization}</a
	>
	{#if session?.permissions.can('knowledge.write', { organization })}
		<a
			class="button primary"
			href={resolve('/organizations/[organization]/knowledge/new', { organization })}>New entry</a
		>
	{/if}
</div>

<form class="form" onsubmit={submit} role="search" aria-label="Search knowledge">
	<label class="grow">Search <input type="search" bind:value={query} /></label>
	<label
		>Kind
		<select bind:value={kind}>
			<option value="">All</option>
			{#each KINDS as k (k)}<option value={k}>{kindLabel[k]}</option>{/each}
		</select>
	</label>
	<label>Project <input bind:value={project} size="8" placeholder="WEB" /></label>
	<button type="submit">Search</button>
</form>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if entries.length === 0}
	<p class="muted">{searched ? `Nothing matches “${searched}”.` : 'No entries yet.'}</p>
{:else}
	<ul class="entries" aria-label={searched ? 'Search results' : 'Entries'}>
		{#each entries as e (e.id)}
			<li class="card">
				<a
					href={resolve('/organizations/[organization]/knowledge/[entry]', {
						organization,
						entry: e.id
					})}>{e.title}</a
				>
				<span class="badge">{kindLabel[e.kind]}</span>
				{#each e.projects as p (p)}<span class="badge mono">{p}</span>{/each}
				<span class="muted small">v{e.version} · {new Date(e.updated_at).toLocaleString()}</span>
			</li>
		{/each}
	</ul>
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
	.entries {
		list-style: none;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.entries li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.5rem;
	}
	.small {
		font-size: 0.85rem;
		margin-left: auto;
	}
</style>
