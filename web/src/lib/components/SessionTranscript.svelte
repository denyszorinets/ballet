<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession } from '$lib/session';
	import { transcript } from '$lib/sessionlog';

	let { run, canManage = false }: { run: Schemas['Run']; canManage?: boolean } = $props();

	let logs = $state<Schemas['RunLog'][]>([]);
	let error = $state<string>();
	const entries = $derived(transcript(logs));
	/** The run as last fetched: its status changes while the transcript is open. */
	let fetched = $state<Schemas['Run']>();
	const current = $derived(fetched?.id === run.id ? fetched : run);
	const active = $derived(['queued', 'starting', 'running'].includes(current.status));
	/** Humans can talk to a running coding-agent session. */
	const talkable = $derived(canManage && !!current.adapter && current.status === 'running');

	let message = $state('');
	let sending = $state(false);
	let notice = $state<string>();

	async function send(kind: 'message' | 'interrupt') {
		sending = true;
		error = notice = undefined;
		const s = await getSession();
		const { error: err } = await s.api.POST('/api/v1/runs/{run}/input', {
			params: { path: { run: run.id } },
			body: { kind, text: message.trim() || undefined }
		});
		sending = false;
		if (err) {
			error = apiError(err);
			return;
		}
		notice =
			kind === 'interrupt'
				? 'Interrupted; the session continues with your message.'
				: 'Sent; the agent reads it when its current turn ends.';
		if (kind === 'interrupt' && !message.trim()) notice = 'Interrupted.';
		message = '';
		await more();
	}

	/** Fetches the run and its output after what is loaded. */
	async function more() {
		const s = await getSession();
		const r = await s.api.GET('/api/v1/runs/{run}', { params: { path: { run: run.id } } });
		if (r.data) fetched = r.data;
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
		{:else if e.kind === 'user'}
			<p class="user"><strong>You:</strong> {e.text}</p>
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
	{#if talkable}
		<form
			class="talk"
			onsubmit={(ev) => {
				ev.preventDefault();
				send('message');
			}}
		>
			<label>
				<span class="visually-hidden">Message to the agent</span>
				<textarea
					rows="2"
					placeholder="Message to the agent"
					bind:value={message}
					disabled={sending}
					onkeydown={(ev) => {
						if (ev.key === 'Enter' && (ev.ctrlKey || ev.metaKey)) {
							ev.preventDefault();
							send('message');
						}
					}}></textarea>
			</label>
			<div class="actions">
				<button type="submit" class="primary" disabled={sending || !message.trim()}>Send</button>
				<button type="button" disabled={sending} onclick={() => send('interrupt')}
					>{message.trim() ? 'Interrupt and send' : 'Interrupt'}</button
				>
			</div>
			{#if notice}<p class="muted small" role="status">{notice}</p>{/if}
		</form>
	{/if}
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
	.talk {
		display: flex;
		flex-direction: column;
		gap: 0.35rem;
		border-top: 1px solid var(--border);
		padding-top: 0.5rem;
	}
	.talk textarea {
		width: 100%;
		box-sizing: border-box;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
	}
	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
		white-space: nowrap;
	}
	.user {
		margin: 0;
		white-space: pre-wrap;
		padding: 0.25rem 0.5rem;
		border-left: 3px solid var(--accent);
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
