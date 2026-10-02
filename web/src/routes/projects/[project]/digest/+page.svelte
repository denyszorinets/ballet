<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { renderMarkdown } from '$lib/markdown';
	import { getSession, type Session } from '$lib/session';

	const project = $derived(page.params.project ?? '');
	const periods = [
		{ hours: 12, label: 'Last 12 hours' },
		{ hours: 24, label: 'Last 24 hours' },
		{ hours: 24 * 7, label: 'Last 7 days' },
		{ hours: 24 * 30, label: 'Last 30 days' }
	];

	let hours = $state(24);
	let digest = $state<Schemas['Digest']>();
	let error = $state<string>();

	async function load(s: Session, key: string, h: number) {
		const since = new Date(Date.now() - h * 3600_000).toISOString();
		const { data, error: err } = await s.api.GET('/api/v1/projects/{project}/digest', {
			params: { path: { project: key }, query: { since } }
		});
		if (!data) {
			error = apiError(err);
			return;
		}
		error = undefined;
		digest = data;
	}

	$effect(() => {
		const key = project;
		const h = hours;
		getSession()
			.then((s) => load(s, key, h))
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	function download() {
		if (!digest) return;
		const url = URL.createObjectURL(new Blob([digest.markdown], { type: 'text/markdown' }));
		const a = document.createElement('a');
		a.href = url;
		a.download = `digest-${project}-${digest.until.slice(0, 10)}.md`;
		a.click();
		URL.revokeObjectURL(url);
	}

	const sessions = $derived(digest ? Object.values(digest.runs).reduce((a, b) => a + b, 0) : 0);
</script>

<svelte:head><title>Digest · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Digest</h1>

<div class="bar">
	<label
		>Period
		<select bind:value={hours}>
			{#each periods as p (p.hours)}<option value={p.hours}>{p.label}</option>{/each}
		</select>
	</label>
	<button onclick={download} disabled={!digest}>Download Markdown</button>
</div>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !digest}
	<p class="muted">Loading…</p>
{:else}
	<ul class="figures" aria-label="Summary">
		<li><strong>{digest.done.length}</strong> done</li>
		<li><strong>{digest.failed.length}</strong> failed</li>
		<li><strong>{digest.merged.length}</strong> merged</li>
		<li><strong>{sessions}</strong> agent sessions</li>
		<li><strong>{digest.tokens.toLocaleString()}</strong> tokens</li>
		<li>
			<strong>{digest.questions_raised}</strong> questions ({digest.answered_by_planner} by the planner,
			{digest.answered_by_human} by humans)
		</li>
		<li><strong>{digest.open_questions.length}</strong> open questions</li>
	</ul>
	<article class="markdown card" aria-label="Digest">
		<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
		{@html renderMarkdown(digest.markdown)}
	</article>
	{#if digest.open_questions.length}
		<p><a href={resolve('/inbox')}>Answer the open questions in the inbox →</a></p>
	{/if}
{/if}

<style>
	.bar {
		display: flex;
		gap: 0.75rem;
		align-items: flex-end;
		margin: 1rem 0;
		flex-wrap: wrap;
	}
	.bar label {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
	}
	.figures {
		list-style: none;
		padding: 0;
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem 1.25rem;
	}
	.figures strong {
		font-size: 1.25rem;
	}
</style>
