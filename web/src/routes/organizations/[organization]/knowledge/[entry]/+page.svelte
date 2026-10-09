<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import KnowledgeEditor, { type Draft } from '$lib/components/KnowledgeEditor.svelte';
	import { kindLabel, type KnowledgeEntry } from '$lib/knowledge';
	import { renderMarkdown } from '$lib/markdown';
	import { getSession, type Session } from '$lib/session';

	type Version = Schemas['KnowledgeVersion'];

	const organization = $derived(page.params.organization ?? '');
	const id = $derived(page.params.entry ?? '');

	let session = $state<Session>();
	let entry = $state<KnowledgeEntry>();
	let versions = $state<Version[]>([]);
	let error = $state<string>();
	let editing = $state(false);
	let saveError = $state<string>();
	/** A past version shown instead of the current body. */
	let viewing = $state<Version>();

	const canWrite = $derived(!!session?.permissions.can('knowledge.write', { organization }));

	async function load(s: Session) {
		const path = { params: { path: { organization, entry: id } } };
		const [e, v] = await Promise.all([
			s.api.GET('/api/v1/organizations/{organization}/knowledge/entries/{entry}', path),
			s.api.GET('/api/v1/organizations/{organization}/knowledge/entries/{entry}/versions', path)
		]);
		if (!e.data) {
			error = apiError(e.error);
			return;
		}
		entry = e.data;
		versions = [...(v.data?.items ?? [])].sort((a, b) => b.version - a.version);
	}

	$effect(() => {
		void id;
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function save(d: Draft) {
		if (!session || !entry) return;
		saveError = undefined;
		const { data, error: err } = await session.api.PATCH(
			'/api/v1/organizations/{organization}/knowledge/entries/{entry}',
			{ params: { path: { organization, entry: id } }, body: { ...d, version: entry.version } }
		);
		if (!data) {
			saveError = apiError(err);
			return;
		}
		editing = false;
		viewing = undefined;
		await load(session);
	}
</script>

<svelte:head><title>{entry?.title ?? 'Knowledge'} · Ballet</title></svelte:head>

<p>
	<a href={resolve('/organizations/[organization]/knowledge', { organization })}>← Knowledge</a>
</p>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !entry}
	<p class="muted">Loading…</p>
{:else if editing}
	<h1>Edit entry</h1>
	<KnowledgeEditor
		initial={entry}
		submitLabel="Save"
		error={saveError}
		onsubmit={save}
		oncancel={() => (editing = false)}
	/>
{:else}
	<div class="title">
		<h1>{viewing?.title ?? entry.title}</h1>
		<span class="badge">{kindLabel[entry.kind]}</span>
		{#if canWrite && !viewing}
			<button
				onclick={() => {
					saveError = undefined;
					editing = true;
				}}>Edit</button
			>
		{/if}
	</div>
	<p class="muted meta">
		Version {entry.version} · updated by {entry.updated_by}
		{new Date(entry.updated_at).toLocaleString()} · created by {entry.created_by}
	</p>
	{#if entry.projects.length || entry.items.length}
		<p class="links">
			{#each entry.projects as p (p)}
				<a class="badge mono" href={resolve('/projects/[project]', { project: p })}>{p}</a>
			{/each}
			{#each entry.items as i (i)}
				<a class="badge mono" href={resolve('/items/[item]', { item: i })}>{i}</a>
			{/each}
		</p>
	{/if}

	{#if viewing}
		<p class="notice" role="status">
			Showing version {viewing.version} by {viewing.author}.
			<button onclick={() => (viewing = undefined)}>Show current</button>
		</p>
	{/if}
	<article class="markdown card" data-testid="body">
		<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
		{@html renderMarkdown(viewing?.body ?? entry.body)}
	</article>

	<h2>Versions</h2>
	<ol class="versions" aria-label="Versions">
		{#each versions as v (v.version)}
			<li>
				<button
					class="link"
					aria-current={(viewing?.version ?? entry.version) === v.version}
					onclick={() => (viewing = v.version === entry?.version ? undefined : v)}
					>v{v.version}</button
				>
				<span>{v.title}</span>
				<span class="muted">{v.author} · {new Date(v.created_at).toLocaleString()}</span>
			</li>
		{/each}
	</ol>
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
	.meta {
		font-size: 0.85rem;
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem;
	}
	.notice {
		display: flex;
		gap: 0.5rem;
		align-items: baseline;
	}
	.versions {
		list-style: none;
		padding: 0;
	}
	.versions li {
		display: flex;
		gap: 0.75rem;
		flex-wrap: wrap;
		align-items: baseline;
		padding: 0.25rem 0;
	}
	.versions [aria-current='true'] {
		font-weight: 700;
	}
	button.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--accent);
		cursor: pointer;
	}
</style>
