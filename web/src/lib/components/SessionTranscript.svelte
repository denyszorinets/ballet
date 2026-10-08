<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession } from '$lib/session';
	import { transcript } from '$lib/sessionlog';

	let { run }: { run: Schemas['Run'] } = $props();

	let logs = $state<Schemas['RunLog'][]>([]);
	let error = $state<string>();
	const entries = $derived(transcript(logs));
	const active = $derived(['queued', 'starting', 'running'].includes(run.status));

	/** Fetches output after what is loaded. */
	async function more() {
		const s = await getSession();
		for (;;) {
			const after = logs.at(-1)?.seq ?? 0;
			const { data, error: err } = await s.api.GET('/api/v1/runs/{run}/logs', {
				params: { path: { run: run.id }, query: { after } }
			});
			if (!data) {
				error = apiError(err);
				return;
			}
			error = undefined;
			logs = [...logs, ...data.items];
			if (data.items.length < 1000) return;
		}
	}

	$effect(() => {
		void run.id;
		more();
		if (!active) return;
		// Live while the session runs.
		const t = setInterval(more, 2000);
		return () => clearInterval(t);
	});
</script>

<section class="transcript" aria-label="Session transcript">
	{#if error}<p class="error small">{error}</p>{/if}
	{#if !entries.length}
		<p class="muted small">{active ? 'Waiting for the session…' : 'No output.'}</p>
	{/if}
	{#each entries as e, i (i)}
		{#if e.kind === 'text'}
			<p class="text">{e.text}</p>
		{:else if e.kind === 'tool_use'}
			<p class="tool small"><code>{e.tool}</code> <span class="muted">{e.input}</span></p>
		{:else if e.kind === 'tool_result'}
			<details class="small">
				<summary class:failed={e.error}>{e.error ? 'tool failed' : 'tool result'}</summary>
				<pre>{e.text}</pre>
			</details>
		{:else if e.kind === 'result'}
			<p class="result" class:failed={e.error}>
				<strong>{e.error ? 'Turn failed' : 'Turn ended'}</strong>{e.text ? `: ${e.text}` : ''}
			</p>
		{:else}
			<pre class="output small" data-stream={e.stream}>{e.text}</pre>
		{/if}
	{/each}
</section>

<style>
	.transcript {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		max-height: 32rem;
		overflow-y: auto;
		padding: 0.5rem;
		border: 1px solid var(--border);
		border-radius: 4px;
	}
	.text,
	.result {
		margin: 0;
		white-space: pre-wrap;
	}
	.tool {
		margin: 0;
		overflow-wrap: anywhere;
	}
	pre {
		margin: 0;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		max-height: 12rem;
		overflow-y: auto;
	}
	.output {
		color: var(--muted);
	}
	.failed {
		color: var(--danger);
	}
	.small {
		font-size: 0.85rem;
	}
</style>
