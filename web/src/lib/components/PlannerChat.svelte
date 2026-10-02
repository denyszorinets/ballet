<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import ChangesetCard from '$lib/components/ChangesetCard.svelte';
	import { renderMarkdown } from '$lib/markdown';
	import { chatEntries, watchSession, type ChatEntry, type PlannerOutput } from '$lib/planner';
	import { getSession, type Session } from '$lib/session';

	let {
		id,
		onload,
		placeholder = 'Describe what you want to build… (Enter sends, Shift+Enter: new line)'
	}: {
		/** The planner session. */
		id: string;
		/** Called with the session after each load. */
		onload?: (info: Schemas['PlannerTranscript']) => void;
		placeholder?: string;
	} = $props();

	let session = $state<Session>();
	let info = $state<Schemas['PlannerTranscript']>();
	let customer = $state<string>();
	let entries = $state<ChatEntry[]>([]);
	let error = $state<string>();
	let turnError = $state<string>();
	let running = $state(false);
	/** Assistant text streamed but not yet stored. */
	let live = $state('');
	/** The tool running now. */
	let liveTool = $state<string>();
	let draft = $state('');
	let sendError = $state<string>();
	let refresh = $state(0);
	let bottom = $state<HTMLElement>();

	const canWrite = $derived(
		!!info && !!session?.permissions.can('tracker.write', { customer, project: info.project })
	);

	async function reload(s: Session) {
		const { data, error: err } = await s.api.GET('/api/v1/planner/sessions/{session}', {
			params: { path: { session: id } }
		});
		if (!data) {
			error = apiError(err);
			return;
		}
		info = data;
		onload?.(data);
		entries = chatEntries(data.messages);
		if (!customer) {
			const p = await s.api.GET('/api/v1/projects/{project}', {
				params: { path: { project: data.project } }
			});
			customer = p.data?.customer;
		}
		requestAnimationFrame(() => bottom?.scrollIntoView({ block: 'end' }));
	}

	function onOutput(s: Session, o: PlannerOutput) {
		switch (o.type) {
			case 'text':
				live += o.text ?? '';
				break;
			case 'tool_call':
				liveTool = o.tool;
				break;
			case 'tool_result':
				liveTool = undefined;
				break;
			case 'message':
				live = '';
				void reload(s);
				break;
			case 'error':
				turnError = o.text;
				break;
			case 'done':
				running = false;
				live = '';
				liveTool = undefined;
				void reload(s);
				break;
		}
	}

	$effect(() => {
		const sessionID = id;
		let stop: (() => void) | undefined;
		let sub: { unsubscribe(): void } | undefined;
		let cancelled = false;
		getSession()
			.then(async (s) => {
				session = s;
				await reload(s);
				if (cancelled || !info) return;
				stop = watchSession(s.realtime, sessionID, {
					onOutput: (o) => onOutput(s, o),
					onReset: (r) => {
						running = r;
						void reload(s);
					}
				});
				sub = s.realtime.subscribe(`project:${info.project}`, {
					onEvent: (e) => {
						if (e.entity_type === 'changeset') refresh++;
					},
					onResync: () => refresh++
				});
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => {
			cancelled = true;
			stop?.();
			sub?.unsubscribe();
		};
	});

	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		if (!session || !draft.trim() || running) return;
		sendError = undefined;
		turnError = undefined;
		const text = draft;
		try {
			running = true;
			draft = '';
			await session.realtime.call('planner.send', { session: id, text });
		} catch (err) {
			running = false;
			draft = text;
			sendError = err instanceof Error ? err.message : String(err);
		}
	}

	function keydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			void send();
		}
	}

	async function cancel() {
		await session?.realtime.call('planner.cancel', { session: id }).catch(() => {});
	}
</script>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !info}
	<p class="muted">Loading…</p>
{:else}
	<div class="chat" aria-label="Conversation" role="log">
		{#each entries as entry, i (i)}
			{#if entry.kind === 'human'}
				<div class="msg human">
					<span class="who muted"
						>{!entry.author || entry.author === session?.auth.subject ? 'You' : entry.author}</span
					>
					<p class="text">{entry.text}</p>
				</div>
			{:else if entry.kind === 'assistant'}
				<div class="msg assistant">
					<span class="who muted">Planner</span>
					<div class="markdown">
						<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
						{@html renderMarkdown(entry.text)}
					</div>
				</div>
			{:else}
				<details class="tool" class:failed={entry.call.isError}>
					<summary>
						<span class="mono">{entry.call.name}</span>
						{#if entry.call.result === undefined}<span class="muted">running…</span>
						{:else if entry.call.isError}<span class="error">failed</span>{/if}
					</summary>
					<pre class="mono">{JSON.stringify(entry.call.input, null, 2)}</pre>
					{#if entry.call.result !== undefined}<pre class="mono">{entry.call.result}</pre>{/if}
				</details>
				{#if entry.call.changeset}
					<ChangesetCard id={entry.call.changeset} canDecide={canWrite} {refresh} />
				{/if}
			{/if}
		{/each}
		{#if live}
			<div class="msg assistant" data-testid="live">
				<span class="who muted">Planner</span>
				<div class="markdown">
					<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
					{@html renderMarkdown(live)}
				</div>
			</div>
		{/if}
		{#if running}
			<p class="muted" role="status" aria-label="Planner status">
				{liveTool ? `Running ${liveTool}…` : 'Planner is answering…'}
			</p>
		{/if}
		{#if turnError}<p class="error" role="alert">{turnError}</p>{/if}
		<div bind:this={bottom}></div>
	</div>

	{#if canWrite}
		<form class="composer" onsubmit={send} aria-label="Message">
			<label class="grow"
				><span class="visually-hidden">Message</span>
				<textarea bind:value={draft} onkeydown={keydown} rows="3" {placeholder}></textarea>
			</label>
			<div class="buttons">
				<button class="primary" type="submit" disabled={running || !draft.trim()}>Send</button>
				{#if running}<button type="button" onclick={cancel}>Stop</button>{/if}
			</div>
			{#if sendError}<p class="error" role="alert">{sendError}</p>{/if}
		</form>
	{/if}
{/if}

<style>
	.chat {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		margin-bottom: 1rem;
	}
	.msg {
		max-width: 48rem;
		padding: 0.5rem 0.75rem;
		border-radius: var(--radius);
		border: 1px solid var(--border);
		background: var(--surface);
	}
	.msg.human {
		align-self: flex-end;
		border-color: var(--accent);
	}
	.who {
		font-size: 0.75rem;
	}
	.text {
		margin: 0.25rem 0 0;
		white-space: pre-wrap;
	}
	.tool {
		font-size: 0.85rem;
		border-left: 3px solid var(--border);
		padding-left: 0.5rem;
	}
	.tool.failed {
		border-left-color: var(--danger);
	}
	.tool pre {
		overflow-x: auto;
		max-height: 16rem;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.composer {
		position: sticky;
		bottom: 0;
		display: flex;
		gap: 0.5rem;
		align-items: flex-end;
		flex-wrap: wrap;
		background: var(--bg);
		padding: 0.5rem 0;
	}
	.composer textarea {
		width: 100%;
		box-sizing: border-box;
		font: inherit;
	}
	.grow {
		flex: 1;
		min-width: 12rem;
	}
	.buttons {
		display: flex;
		gap: 0.5rem;
	}
	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}
</style>
