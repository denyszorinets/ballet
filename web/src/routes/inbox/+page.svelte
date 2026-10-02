<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import PlannerChat from '$lib/components/PlannerChat.svelte';
	import { renderMarkdown } from '$lib/markdown';
	import { getSession, type Session } from '$lib/session';

	type Entry = Schemas['InboxEntry'];

	let session = $state<Session>();
	let entries = $state<Entry[]>([]);
	let loaded = $state(false);
	let error = $state<string>();
	let answer = $state('');
	let answerError = $state<string>();
	let answering = $state(false);
	let chatError = $state<string>();

	/** The question shown; from ?q= at first, else the most impactful. */
	let chosen = $state<string | null>(page.url.searchParams.get('q'));
	const selected = $derived(entries.find((e) => e.id === chosen) ?? entries[0]);
	const canAnswer = $derived(
		!!selected &&
			!!session?.permissions.can('tracker.write', {
				customer: selected.customer,
				project: selected.project
			})
	);

	async function load(s: Session) {
		const { data, error: err } = await s.api.GET('/api/v1/inbox');
		if (data) {
			entries = data.items;
			error = undefined;
		} else error = apiError(err);
		loaded = true;
	}

	$effect(() => {
		let timer: ReturnType<typeof setInterval> | undefined;
		getSession()
			.then((s) => {
				session = s;
				void load(s);
				timer = setInterval(() => void load(s), 15_000);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => clearInterval(timer);
	});

	function select(id: string) {
		answer = '';
		answerError = undefined;
		chatError = undefined;
		chosen = id;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!session || !selected || !answer.trim()) return;
		answering = true;
		answerError = undefined;
		const { data, error: err } = await session.api.POST('/api/v1/questions/{question}/answer', {
			params: { path: { question: selected.id } },
			body: { answer }
		});
		answering = false;
		if (!data) {
			answerError = apiError(err);
			return;
		}
		const i = entries.findIndex((x) => x.id === selected.id);
		const next = entries[i + 1] ?? entries[i - 1];
		entries = entries.filter((x) => x.id !== data.id);
		answer = '';
		if (next) select(next.id);
	}

	async function discuss() {
		if (!session || !selected) return;
		chatError = undefined;
		const { data, error: err } = await session.api.POST('/api/v1/questions/{question}/chat', {
			params: { path: { question: selected.id } }
		});
		if (!data) {
			chatError = apiError(err);
			return;
		}
		const id = selected.id;
		entries = entries.map((x) => (x.id === id ? { ...x, chat: data.id } : x));
	}

	function waiting(since: string): string {
		const minutes = Math.max(0, Math.round((Date.now() - new Date(since).getTime()) / 60_000));
		if (minutes < 60) return `${minutes} min`;
		const hours = Math.round(minutes / 60);
		return hours < 48 ? `${hours} h` : `${Math.round(hours / 24)} d`;
	}
</script>

<svelte:head><title>Inbox · Ballet</title></svelte:head>

<h1>Inbox</h1>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if entries.length === 0}
	<p class="muted">No open questions. Agents are working on their own.</p>
{:else}
	<div class="inbox">
		<ul class="list" aria-label="Open questions">
			{#each entries as e (e.id)}
				<li>
					<button
						class="entry"
						class:active={e.id === selected?.id}
						aria-current={e.id === selected?.id ? 'true' : undefined}
						onclick={() => select(e.id)}
					>
						<span class="meta muted small">
							{e.ticket} · waiting {waiting(e.created_at)}
						</span>
						<span class="q">{e.text}</span>
						<span class="badges">
							{#if e.blocking}<span class="badge danger">blocking</span>{/if}
							{#if e.blocked_behind > 0}<span class="badge">{e.blocked_behind} waiting behind</span
								>{/if}
							{#if e.route === 'planner'}<span class="badge">planner is looking</span>{/if}
						</span>
					</button>
				</li>
			{/each}
		</ul>

		{#if selected}
			<section class="detail" aria-label="Question">
				<p class="muted small">
					<a href={resolve('/items/[item]', { item: selected.ticket })}
						>{selected.ticket} · {selected.ticket_title}</a
					>
					· {selected.ticket_state.replaceAll('_', ' ')}
					{#if !selected.run}· asked by Ballet about the pipeline{/if}
				</p>
				<h2 class="question">{selected.text}</h2>
				{#if selected.context}
					<div class="markdown context">
						<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
						{@html renderMarkdown(selected.context)}
					</div>
				{/if}

				{#if canAnswer}
					<form onsubmit={submit} aria-label="Answer">
						<label
							>Your answer
							<textarea
								bind:value={answer}
								rows="4"
								placeholder="The answer the agent continues with"></textarea>
						</label>
						<div class="actions">
							<button class="primary" type="submit" disabled={answering || !answer.trim()}
								>Answer and resume</button
							>
						</div>
						{#if answerError}<p class="error" role="alert">{answerError}</p>{/if}
					</form>

					<h3>Discuss with the planner</h3>
					{#if selected.chat}
						{#key selected.chat}
							<PlannerChat
								id={selected.chat}
								placeholder="Ask the planner about this question… (Enter sends)"
							/>
						{/key}
					{:else}
						<button onclick={discuss}>Open a sub-chat</button>
						{#if chatError}<p class="error" role="alert">{chatError}</p>{/if}
					{/if}
				{:else}
					<p class="muted">You can read this question but not answer it.</p>
				{/if}
			</section>
		{/if}
	</div>
{/if}

<style>
	.inbox {
		display: grid;
		grid-template-columns: minmax(16rem, 22rem) 1fr;
		gap: 1.5rem;
		align-items: start;
	}
	@media (max-width: 760px) {
		.inbox {
			grid-template-columns: 1fr;
		}
	}
	.list {
		list-style: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.entry {
		width: 100%;
		text-align: left;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		padding: 0.5rem 0.75rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		font: inherit;
		color: inherit;
		cursor: pointer;
	}
	.entry.active {
		border-color: var(--accent);
	}
	.q {
		overflow: hidden;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
	}
	.badges {
		display: flex;
		gap: 0.25rem;
		flex-wrap: wrap;
	}
	.badge.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
	.question {
		white-space: pre-wrap;
		font-size: 1.15rem;
	}
	textarea {
		width: 100%;
		box-sizing: border-box;
		font: inherit;
	}
	.detail {
		min-width: 0;
	}
	.small {
		font-size: 0.85rem;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		margin-top: 0.5rem;
	}
</style>
