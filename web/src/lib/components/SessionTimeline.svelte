<script lang="ts">
	import type { Schemas } from '$lib/api/client';
	import SessionTranscript from './SessionTranscript.svelte';

	type Run = Schemas['Run'];
	type Report = Schemas['Report'];
	type Question = Schemas['Question'];
	type Event = Schemas['Event'];
	type Usage = Schemas['UsageTotals'];

	let {
		runs,
		reports,
		questions,
		history,
		usage
	}: {
		runs: Run[];
		reports: Report[];
		questions: Question[];
		history: Event[];
		/** Usage grouped by caller ("run:<id>", "planner:<session>"). */
		usage: Usage[];
	} = $props();

	type Entry =
		| { kind: 'run'; at: string; run: Run }
		| { kind: 'wait'; at: string; reason: string; stage: string }
		| { kind: 'resume'; at: string; stage: string };

	const waitLabel: Record<string, string> = {
		approval: 'Waited for a human approval',
		checks: 'Waited for checks',
		review: 'Waited for a human review',
		merge: 'Waited for the merge',
		answer: 'Waited for answers',
		pause: 'Held: work paused',
		budget: 'Held: out of budget'
	};

	const entries = $derived.by(() => {
		const out: Entry[] = runs.map((r) => ({ kind: 'run', at: r.created_at, run: r }));
		for (const e of history) {
			const p = (e.payload ?? {}) as Record<string, unknown>;
			if (e.type === 'flow.waiting' && p.for !== 'checks')
				out.push({
					kind: 'wait',
					at: e.occurred_at,
					reason: String(p.for ?? ''),
					stage: String(p.stage ?? '')
				});
			if (e.type === 'flow.resumed')
				out.push({ kind: 'resume', at: e.occurred_at, stage: String(p.stage ?? '') });
		}
		return out.sort((a, b) => a.at.localeCompare(b.at));
	});

	/** Runs whose transcript is shown. */
	let shown = $state<Record<string, boolean>>({});

	const byCaller = $derived(new Map(usage.map((u) => [u.key ?? '', u])));
	const total = $derived(
		usage.reduce((n, u) => n + u.input_tokens + u.output_tokens + u.cache_write_tokens, 0)
	);

	function duration(r: Run): string {
		if (!r.started_at) return '';
		const end = r.finished_at ? new Date(r.finished_at).getTime() : Date.now();
		const s = Math.max(0, Math.round((end - new Date(r.started_at).getTime()) / 1000));
		if (s < 60) return `${s} s`;
		const m = Math.round(s / 60);
		return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
	}

	const fmt = (n: number) => n.toLocaleString();
	const stageReport = (r: Run) =>
		[...reports].reverse().find((x) => x.run === r.id && x.kind === 'stage_report');
	const assumptions = (r: Run) => reports.filter((x) => x.run === r.id && x.kind === 'assumption');
	const asked = (r: Run) => questions.filter((q) => q.run === r.id);
</script>

<section aria-label="Timeline">
	<h2>Timeline</h2>
	{#if usage.length}
		<p class="muted small">
			{fmt(total)} counted tokens on this ticket (input, output and cache writes).
		</p>
	{/if}
	<ol class="timeline">
		{#each entries as e, i (i)}
			{#if e.kind === 'run'}
				{@const u = byCaller.get(`run:${e.run.id}`)}
				{@const rep = stageReport(e.run)}
				<li class="session card" aria-label="Session {e.run.stage}">
					<div class="head">
						<strong>{e.run.stage}</strong>
						<span class="badge" data-status={e.run.status}>{e.run.status}</span>
						{#if rep?.outcome}<span class="badge">outcome: {rep.outcome}</span>{/if}
						<span class="muted small">
							{new Date(e.run.created_at).toLocaleString()}
							{#if duration(e.run)}· {duration(e.run)}{/if}
							{#if e.run.adapter}· {e.run.adapter}{/if}
						</span>
					</div>
					{#if u}
						<p class="small tokens">
							{fmt(u.input_tokens)} in · {fmt(u.output_tokens)} out · {fmt(u.cache_write_tokens)} cache
							write · {fmt(u.cache_read_tokens)} cache read · {u.requests} requests
						</p>
					{/if}
					{#if rep}
						<p class="report">{rep.text}</p>
					{:else if e.run.result?.summary || e.run.error}
						<p class="report muted">{e.run.result?.summary ?? e.run.error}</p>
					{/if}
					{#each assumptions(e.run) as a (a.id)}
						<p class="small">
							<span class="badge">assumption{a.review ? `: ${a.review}` : ''}</span>
							{a.text}
						</p>
					{/each}
					<button
						type="button"
						class="link small"
						aria-expanded={!!shown[e.run.id]}
						onclick={() => (shown[e.run.id] = !shown[e.run.id])}
						>{shown[e.run.id] ? 'Hide' : 'Show'} {e.run.adapter ? 'session' : 'output'}</button
					>
					{#if shown[e.run.id]}<SessionTranscript run={e.run} />{/if}
					{#each asked(e.run) as q (q.id)}
						<p class="small">
							<span class="badge" class:blocking={q.blocking}
								>{q.blocking ? 'blocking question' : 'question'}</span
							>
							{q.text.split('\n')[0]}
							{#if q.status === 'answered'}<span class="muted"
									>— answered by {q.answered_by}: {q.answer}</span
								>{:else}<span class="muted">— open</span>{/if}
						</p>
					{/each}
				</li>
			{:else if e.kind === 'wait'}
				<li class="event muted small">
					{waitLabel[e.reason] ?? `Waited (${e.reason})`}{e.stage ? ` at ${e.stage}` : ''} · {new Date(
						e.at
					).toLocaleString()}
				</li>
			{:else}
				<li class="event muted small">
					Resumed{e.stage ? ` at ${e.stage}` : ''} · {new Date(e.at).toLocaleString()}
				</li>
			{/if}
		{/each}
	</ol>
</section>

<style>
	.timeline {
		list-style: none;
		padding: 0 0 0 0.75rem;
		margin: 0;
		border-left: 2px solid var(--border);
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.session {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.head {
		display: flex;
		gap: 0.5rem;
		align-items: center;
		flex-wrap: wrap;
	}
	.report {
		margin: 0;
		white-space: pre-wrap;
	}
	.tokens {
		margin: 0;
		font-variant-numeric: tabular-nums;
	}
	.small {
		font-size: 0.85rem;
	}
	.event {
		padding-left: 0.25rem;
	}
	button.link {
		align-self: flex-start;
		background: none;
		border: none;
		padding: 0;
		color: var(--accent);
		cursor: pointer;
	}
	.badge.blocking {
		border-color: var(--danger);
		color: var(--danger);
	}
</style>
