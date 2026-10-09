<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import Diff from '$lib/components/Diff.svelte';
	import { diffFiles } from '$lib/diff';
	import { renderMarkdown } from '$lib/markdown';
	import { getSession, type Session } from '$lib/session';
	import { contentFiles, formatScope, parseScope, scopeTarget } from '$lib/skills';

	type Skill = Schemas['Skill'];
	type Version = Schemas['SkillVersion'];

	const id = $derived(page.params.skill ?? '');

	let session = $state<Session>();
	let skill = $state<Skill>();
	let versions = $state<Version[]>([]);
	let projectCustomer = $state<string>();
	let error = $state<string>();
	let actionError = $state<string>();
	let notice = $state<string>();

	let description = $state('');
	let body = $state('');
	let files = $state<{ path: string; content: string }[]>([]);
	let preview = $state(false);
	/** The version shown with its diff against the version before it. */
	let selected = $state<Version>();

	const scope = $derived(parseScope(skill?.scope ?? 'platform'));
	const canWrite = $derived(
		!!session?.permissions.can('skill.write', scopeTarget(scope, projectCustomer))
	);
	const latest = $derived(versions[0]);
	const draftFiles = $derived(Object.fromEntries(files.map((f) => [f.path, f.content])));
	const dirty = $derived(
		!!skill &&
			(description !== skill.description ||
				body !== skill.body ||
				JSON.stringify(draftFiles) !== JSON.stringify(skill.files) ||
				Object.keys(draftFiles).length !== files.length)
	);
	/** Saved draft against the latest published version (or nothing). */
	const unpublished = $derived(
		skill
			? diffFiles(
					latest ? contentFiles(latest) : {},
					contentFiles({ description: skill.description, body: skill.body, files: skill.files })
				)
			: []
	);

	function reset(s: Skill) {
		description = s.description;
		body = s.body;
		files = Object.entries(s.files)
			.sort(([a], [b]) => a.localeCompare(b))
			.map(([path, content]) => ({ path, content }));
	}

	async function load(s: Session, k: string) {
		const path = { params: { path: { skill: k } } };
		const [sk, v] = await Promise.all([
			s.api.GET('/api/v1/skills/{skill}', path),
			s.api.GET('/api/v1/skills/{skill}/versions', path)
		]);
		if (!sk.data) {
			error = apiError(sk.error);
			return;
		}
		skill = sk.data;
		versions = [...(v.data?.items ?? [])].sort((a, b) => b.number - a.number);
		reset(sk.data);
		const sc = parseScope(sk.data.scope);
		if (sc.kind === 'project' && !projectCustomer) {
			const p = await s.api.GET('/api/v1/projects/{project}', {
				params: { path: { project: sc.key } }
			});
			projectCustomer = p.data?.customer;
		}
	}

	$effect(() => {
		const k = id;
		getSession()
			.then((s) => {
				session = s;
				return load(s, k);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function save(): Promise<boolean> {
		if (!session || !skill) return false;
		actionError = undefined;
		notice = undefined;
		if (Object.keys(draftFiles).length !== files.length) {
			actionError = 'Two files have the same path.';
			return false;
		}
		const { data, error: err } = await session.api.PATCH('/api/v1/skills/{skill}', {
			params: { path: { skill: id } },
			body: { version: skill.version, description, body, files: draftFiles }
		});
		if (!data) {
			actionError = apiError(err);
			return false;
		}
		skill = data;
		reset(data);
		notice = 'Draft saved.';
		return true;
	}

	async function publish() {
		if (!session || !skill) return;
		if (dirty && !(await save())) return;
		const { data, error: err } = await session.api.POST('/api/v1/skills/{skill}/publish', {
			params: { path: { skill: id } },
			body: { version: skill.version }
		});
		if (!data) {
			actionError = apiError(err);
			return;
		}
		await load(session, id);
		notice = `Published v${data.number}.`;
	}

	function previous(v: Version): Version | undefined {
		return versions.find((x) => x.number < v.number);
	}
</script>

<svelte:head><title>{skill?.name ?? 'Skill'} · Skills · Ballet</title></svelte:head>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !skill}
	<p class="muted">Loading…</p>
{:else}
	<!-- eslint-disable svelte/no-navigation-without-resolve -- resolved path plus a query -->
	<p>
		<a href={`${resolve('/skills')}?scope=${encodeURIComponent(formatScope(scope))}`}
			>← Skills ({skill.scope})</a
		>
	</p>
	<!-- eslint-enable svelte/no-navigation-without-resolve -->
	<div class="title">
		<h1 class="mono">{skill.name}</h1>
		<span class="badge">{skill.scope}</span>
		<span class="badge" data-testid="latest"
			>{skill.latest_version ? `v${skill.latest_version}` : 'unpublished'}</span
		>
	</div>

	<h2>Draft</h2>
	{#if canWrite}
		<form
			class="editor"
			aria-label="Skill draft"
			onsubmit={(e) => {
				e.preventDefault();
				void save();
			}}
		>
			<label>Description <input bind:value={description} required /></label>
			<div class="tabs" role="tablist" aria-label="SKILL.md view">
				<button
					type="button"
					role="tab"
					aria-selected={!preview}
					class:active={!preview}
					onclick={() => (preview = false)}>Write</button
				>
				<button
					type="button"
					role="tab"
					aria-selected={preview}
					class:active={preview}
					onclick={() => (preview = true)}>Preview</button
				>
			</div>
			{#if preview}
				<div class="markdown card" data-testid="preview">
					<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
					{@html renderMarkdown(body)}
				</div>
			{:else}
				<label>SKILL.md <textarea bind:value={body} rows="14"></textarea></label>
			{/if}

			<h3>Files</h3>
			{#each files as f, i (i)}
				<fieldset class="file">
					<legend class="visually-hidden">File {i + 1}</legend>
					<label
						>Path <input bind:value={f.path} required placeholder="references/guide.md" /></label
					>
					<label>Content <textarea bind:value={f.content} rows="5"></textarea></label>
					<button type="button" onclick={() => files.splice(i, 1)}>Remove file</button>
				</fieldset>
			{/each}
			<div class="actions">
				<button type="button" onclick={() => files.push({ path: '', content: '' })}>Add file</button
				>
			</div>

			<div class="actions">
				<button type="submit" disabled={!dirty}>Save draft</button>
				<button
					class="primary"
					type="button"
					disabled={!dirty && unpublished.length === 0}
					onclick={publish}>{dirty ? 'Save and publish' : 'Publish'}</button
				>
				{#if dirty}<button type="button" onclick={() => skill && reset(skill)}>Discard</button>{/if}
			</div>
			{#if actionError}<p class="error" role="alert">{actionError}</p>{/if}
			{#if notice}<p class="ok">{notice}</p>{/if}
		</form>
	{:else}
		<p>{skill.description}</p>
		<article class="markdown card">
			<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
			{@html renderMarkdown(skill.body)}
		</article>
	{/if}

	{#if unpublished.length > 0}
		<h2>Unpublished changes</h2>
		<p class="muted">
			Saved draft compared with {latest ? `v${latest.number}` : 'nothing published'}.
		</p>
		{#each unpublished as f (f.path)}
			<h3 class="mono">{f.path} <span class="badge">{f.status}</span></h3>
			<Diff lines={f.lines} label={`Unpublished changes to ${f.path}`} />
		{/each}
	{/if}

	<h2>Versions</h2>
	{#if versions.length === 0}
		<p class="muted">Not published yet.</p>
	{:else}
		<ol class="versions" aria-label="Versions">
			{#each versions as v (v.number)}
				<li>
					<button
						class="link"
						aria-current={selected?.number === v.number}
						onclick={() => (selected = selected?.number === v.number ? undefined : v)}
						>v{v.number}</button
					>
					<span>{v.description}</span>
					<span class="muted">{v.published_by} · {new Date(v.published_at).toLocaleString()}</span>
				</li>
			{/each}
		</ol>
	{/if}

	{#if selected}
		{@const prev = previous(selected)}
		<h3>
			v{selected.number}
			{prev ? `compared with v${prev.number}` : '(first version)'}
		</h3>
		{#each diffFiles(prev ? contentFiles(prev) : {}, contentFiles(selected)) as f (f.path)}
			<h4 class="mono">{f.path} <span class="badge">{f.status}</span></h4>
			<Diff lines={f.lines} label={`Changes to ${f.path} in v${selected.number}`} />
		{/each}
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
	.editor {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.editor label {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	textarea {
		width: 100%;
		box-sizing: border-box;
		font-family: ui-monospace, monospace;
	}
	.file {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		align-items: flex-start;
	}
	.file label {
		align-self: stretch;
	}
	.tabs,
	.actions {
		display: flex;
		gap: 0.5rem;
	}
	.tabs .active {
		font-weight: 600;
		border-color: currentColor;
	}
	.ok {
		color: var(--ok);
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
	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}
</style>
