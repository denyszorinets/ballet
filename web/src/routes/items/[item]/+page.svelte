<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';
	import SessionTimeline from '$lib/components/SessionTimeline.svelte';
	import { nextStates, stateLabel, type Item, type ItemState } from '$lib/tracker';

	type Dependency = Schemas['Dependency'];
	type Event = Schemas['Event'];

	const key = $derived(page.params.item ?? '');

	let session = $state<Session>();
	let item = $state<Item>();
	let customer = $state<string>();
	let containers = $state<Item[]>([]);
	let deps = $state<Dependency[]>([]);
	let history = $state<Event[]>([]);
	let knowledge = $state<Schemas['KnowledgeEntry'][]>([]);
	let runs = $state<Schemas['Run'][]>([]);
	let usage = $state<Schemas['UsageTotals'][]>([]);
	let reports = $state<Schemas['Report'][]>([]);
	let questions = $state<Schemas['Question'][]>([]);
	let pr = $state<Schemas['PullRequest']>();
	let flow = $state<Schemas['Flow']>();
	let flowError = $state<string>();
	let prError = $state<string>();
	let prBusy = $state(false);
	const itemQuery = $derived(`?item=${encodeURIComponent(key)}`);
	let error = $state<string>();
	let actionError = $state<string>();

	let editing = $state(false);
	let draft = $state({
		title: '',
		description: '',
		type: 'feature' as Schemas['TicketType'],
		criteria: '',
		review: 'agent' as Schemas['Policy']['review_mode'],
		merge: 'auto' as Schemas['Policy']['merge_mode'],
		epic: '',
		milestone: ''
	});
	let depForm = $state({ type: 'blocked_by' as Schemas['DependencyType'], item: '' });

	const canWrite = $derived(
		!!item && !!session?.permissions.can('tracker.write', { customer, project: item.project })
	);

	async function flowAction(action: 'start' | 'approve' | 'reject') {
		if (!session) return;
		flowError = undefined;
		const path = { params: { path: { item: key } } };
		const res =
			action === 'start'
				? await session.api.POST('/api/v1/items/{item}/flow/start', path)
				: action === 'approve'
					? await session.api.POST('/api/v1/items/{item}/flow/approve', { ...path, body: {} })
					: await session.api.POST('/api/v1/items/{item}/flow/reject', { ...path, body: {} });
		if (res.data) flow = res.data;
		else flowError = apiError(res.error);
	}

	async function prAction(action: 'open' | 'refresh' | 'merge') {
		if (!session) return;
		prBusy = true;
		prError = undefined;
		const path = { params: { path: { item: key } } };
		try {
			const res =
				action === 'open'
					? await session.api.POST('/api/v1/items/{item}/pull-request', path)
					: action === 'refresh'
						? await session.api.POST('/api/v1/items/{item}/pull-request/refresh', path)
						: await session.api.POST('/api/v1/items/{item}/pull-request/merge', path);
			if (res.data) pr = res.data;
			else prError = apiError(res.error);
		} finally {
			prBusy = false;
		}
	}

	async function load(s: Session, k: string) {
		const path = { params: { path: { item: k } } };
		const [it, d, h, rn, rp, q, pp, fw] = await Promise.all([
			s.api.GET('/api/v1/items/{item}', path),
			s.api.GET('/api/v1/items/{item}/dependencies', path),
			s.api.GET('/api/v1/items/{item}/history', path),
			s.api.GET('/api/v1/items/{item}/runs', path),
			s.api.GET('/api/v1/items/{item}/reports', path),
			s.api.GET('/api/v1/items/{item}/questions', path),
			s.api.GET('/api/v1/items/{item}/pull-request', path),
			s.api.GET('/api/v1/items/{item}/flow', path)
		]);
		if (!it.data) {
			error = apiError(it.error);
			return;
		}
		item = it.data;
		deps = d.data?.items ?? [];
		history = h.data?.items ?? [];
		runs = rn.data?.items ?? [];
		reports = rp.data?.items ?? [];
		questions = q.data?.items ?? [];
		pr = pp.data;
		flow = fw.data;
		const u = await s.api.GET('/api/v1/projects/{project}/usage', {
			params: { path: { project: it.data.project }, query: { group_by: 'run', ticket: k } }
		});
		usage = u.data?.items ?? [];
		if (!customer || containers.length === 0) {
			const pp = { params: { path: { project: it.data.project } } };
			const [p, list] = await Promise.all([
				s.api.GET('/api/v1/projects/{project}', pp),
				s.api.GET('/api/v1/projects/{project}/items', pp)
			]);
			customer = p.data?.customer;
			containers = (list.data?.items ?? []).filter((i) => i.kind !== 'ticket');
		}
		if (customer && s.permissions.can('knowledge.read', { customer })) {
			const kn = await s.api.GET('/api/v1/customers/{customer}/knowledge/entries', {
				params: { path: { customer }, query: { item: k } }
			});
			knowledge = kn.data?.items ?? [];
		}
	}

	$effect(() => {
		const k = key;
		let sub: { unsubscribe(): void } | undefined;
		let cancelled = false;
		getSession()
			.then(async (s) => {
				session = s;
				sub = s.realtime.subscribe(`item:${k}`, {
					onEvent: () => void load(s, k),
					onResync: () => void load(s, k)
				});
				if (cancelled) sub.unsubscribe();
				await load(s, k);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => {
			cancelled = true;
			sub?.unsubscribe();
		};
	});

	function startEdit() {
		if (!item) return;
		draft = {
			title: item.title,
			description: item.description,
			type: item.type ?? 'feature',
			criteria: (item.acceptance_criteria ?? []).join('\n'),
			review: item.policy?.review_mode ?? 'agent',
			merge: item.policy?.merge_mode ?? 'auto',
			epic: item.epic ?? '',
			milestone: item.milestone ?? ''
		};
		actionError = undefined;
		editing = true;
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!session || !item) return;
		const body: Schemas['UpdateItem'] = {
			version: item.version,
			title: draft.title,
			description: draft.description
		};
		if (item.kind !== 'milestone') body.milestone = draft.milestone;
		if (item.kind === 'ticket') {
			body.type = draft.type;
			body.acceptance_criteria = draft.criteria
				.split('\n')
				.map((c) => c.trim())
				.filter(Boolean);
			body.policy = { review_mode: draft.review, merge_mode: draft.merge };
			body.epic = draft.epic;
		}
		const { data, error: err } = await session.api.PATCH('/api/v1/items/{item}', {
			params: { path: { item: key } },
			body
		});
		if (err) {
			actionError = apiError(err);
			return;
		}
		item = data;
		editing = false;
	}

	async function transition(to: ItemState) {
		if (!session || !item) return;
		actionError = undefined;
		const { data, error: err } = await session.api.POST('/api/v1/items/{item}/transition', {
			params: { path: { item: key } },
			body: { state: to, version: item.version }
		});
		if (err) actionError = apiError(err);
		else item = data;
	}

	async function addDependency(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		actionError = undefined;
		const { error: err } = await session.api.POST('/api/v1/items/{item}/dependencies', {
			params: { path: { item: key } },
			body: { type: depForm.type, item: depForm.item.trim() }
		});
		if (err) {
			actionError = apiError(err);
			return;
		}
		depForm = { ...depForm, item: '' };
		await load(session, key);
	}

	async function removeDependency(d: Dependency) {
		if (!session) return;
		const { error: err } = await session.api.DELETE('/api/v1/dependencies/{id}', {
			params: { path: { id: d.id } }
		});
		if (err) actionError = apiError(err);
		await load(session, key);
	}

	const depLabel: Record<Schemas['DependencyType'], string> = {
		blocks: 'Blocks',
		blocked_by: 'Blocked by',
		relates: 'Relates to'
	};
</script>

<svelte:head><title>{key} · Ballet</title></svelte:head>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !item}
	<p class="muted">Loading…</p>
{:else}
	<nav class="crumbs" aria-label="Breadcrumb">
		<a href={resolve('/projects/[project]', { project: item.project })}>{item.project} board</a>
	</nav>

	{#if editing}
		<form class="card edit" onsubmit={save} aria-label="Edit item">
			<label>Title <input bind:value={draft.title} required /></label>
			<label>Description <textarea bind:value={draft.description} rows="5"></textarea></label>
			{#if item.kind === 'ticket'}
				<label
					>Acceptance criteria (one per line)
					<textarea bind:value={draft.criteria} rows="4"></textarea></label
				>
				<div class="form">
					<label
						>Type
						<select bind:value={draft.type}>
							{#each ['feature', 'bug', 'tech_debt', 'docs', 'spike'] as const as t (t)}
								<option value={t}>{t}</option>
							{/each}
						</select>
					</label>
					<label
						>Review
						<select bind:value={draft.review}>
							<option value="agent">agent</option>
							<option value="agent+human">agent+human</option>
						</select>
					</label>
					<label
						>Merge
						<select bind:value={draft.merge}>
							<option value="auto">auto</option>
							<option value="manual">manual</option>
						</select>
					</label>
					<label
						>Epic
						<select bind:value={draft.epic}>
							<option value="">none</option>
							{#each containers.filter((c) => c.kind === 'epic') as c (c.key)}
								<option value={c.key}>{c.key} {c.title}</option>
							{/each}
						</select>
					</label>
				</div>
			{/if}
			{#if item.kind !== 'milestone'}
				<label
					>Milestone
					<select bind:value={draft.milestone}>
						<option value="">none</option>
						{#each containers.filter((c) => c.kind === 'milestone') as c (c.key)}
							<option value={c.key}>{c.key} {c.title}</option>
						{/each}
					</select>
				</label>
			{/if}
			<div class="form">
				<button class="primary" type="submit">Save</button>
				<button type="button" onclick={() => (editing = false)}>Cancel</button>
			</div>
		</form>
	{:else}
		<div class="title">
			<span class="mono muted">{item.key}</span>
			<h1>{item.title}</h1>
		</div>
		<p class="meta">
			<span class="badge">{item.kind}</span>
			{#if item.type}<span class="badge">{item.type}</span>{/if}
			<span class="badge" data-testid="state">{stateLabel[item.state]}</span>
			{#if item.stage}<span class="badge">stage: {item.stage}</span>{/if}
			{#if item.epic}<span class="muted">Epic {item.epic}</span>{/if}
			{#if item.milestone}<span class="muted">Milestone {item.milestone}</span>{/if}
			{#if item.policy}
				<span class="muted">Review {item.policy.review_mode} · Merge {item.policy.merge_mode}</span>
			{/if}
		</p>
		{#if canWrite}
			<div class="actions">
				<button onclick={startEdit}>Edit</button>
				{#each nextStates(item.kind, item.state) as to (to)}
					<button onclick={() => transition(to)}>Move to {stateLabel[to]}</button>
				{/each}
			</div>
		{/if}
		{#if item.description}<p class="description">{item.description}</p>{/if}
		{#if item.acceptance_criteria?.length}
			<h2>Acceptance criteria</h2>
			<ul>
				{#each item.acceptance_criteria as c, i (i)}<li>{c}</li>{/each}
			</ul>
		{/if}
	{/if}

	{#if actionError}<p class="error" role="alert">{actionError}</p>{/if}

	<h2>Dependencies</h2>
	{#if deps.length === 0}
		<p class="muted">No dependencies.</p>
	{:else}
		<ul class="deps">
			{#each deps as d (d.id)}
				<li>
					<span class="dep-type">{depLabel[d.type]}</span>
					<a href={resolve('/items/[item]', { item: d.item.key })}>
						<span class="mono">{d.item.key}</span>
						{d.item.title}</a
					>
					<span class="muted">{stateLabel[d.item.state]}</span>
					{#if canWrite}
						<button
							onclick={() => removeDependency(d)}
							aria-label={`Remove dependency on ${d.item.key}`}>Remove</button
						>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
	{#if canWrite}
		<form class="form" onsubmit={addDependency} aria-label="Add dependency">
			<label
				>Relation
				<select bind:value={depForm.type}>
					<option value="blocked_by">Blocked by</option>
					<option value="blocks">Blocks</option>
					<option value="relates">Relates to</option>
				</select>
			</label>
			<label>Item key <input bind:value={depForm.item} required placeholder="WEB-1" /></label>
			<button type="submit">Add dependency</button>
		</form>
	{/if}

	{#if customer && session?.permissions.can('knowledge.read', { customer })}
		<h2>Knowledge</h2>
		{#if knowledge.length === 0}
			<p class="muted">No linked knowledge.</p>
		{:else}
			<ul aria-label="Linked knowledge">
				{#each knowledge as e (e.id)}
					<li>
						<a href={resolve('/customers/[customer]/knowledge/[entry]', { customer, entry: e.id })}
							>{e.title}</a
						>
						<span class="badge">{e.kind}</span>
					</li>
				{/each}
			</ul>
		{/if}
		{#if session.permissions.can('knowledge.write', { customer })}
			<!-- eslint-disable svelte/no-navigation-without-resolve -- resolved path plus a query -->
			<a href={resolve('/customers/[customer]/knowledge/new', { customer }) + itemQuery}
				>Add knowledge</a
			>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/if}
	{/if}

	{#if item.kind === 'ticket'}
		<h2>Pipeline</h2>
		{#if flow}
			<ol class="stages" aria-label="Pipeline stages">
				{#each flow.stages as st (st.id)}
					<li class:current={st.id === flow.stage}>
						{st.name || st.id}<span class="muted small"> · {st.kind}</span>
					</li>
				{/each}
			</ol>
			<p data-testid="flow-status">
				<span class="badge">{flow.status}</span>
				{#if flow.waiting}<span class="muted">waiting for {flow.waiting}</span>{/if}
				{#if flow.iteration}<span class="muted small"
						>iteration {flow.iteration} of {flow.max_iterations}</span
					>{/if}
				<span class="muted small">pipeline {flow.pipeline} v{flow.pipeline_version}</span>
			</p>
			{#if flow.waiting === 'approval' && canWrite}
				<div class="actions">
					<button class="primary" onclick={() => flowAction('approve')}>Approve stage</button>
					<button onclick={() => flowAction('reject')}>Reject stage</button>
				</div>
			{/if}
			{#if flow.report}
				<details>
					<summary class="muted small">Last stage report</summary>
					<div class="markdown">{flow.report}</div>
				</details>
			{/if}
		{:else}
			<p class="muted">Not started.</p>
		{/if}
		{#if item.state === 'ready' && (!flow || ['done', 'failed', 'stopped'].includes(flow.status)) && session?.permissions.can( 'run.manage', { customer, project: item.project } )}
			<button onclick={() => flowAction('start')}>Start pipeline</button>
		{/if}
		{#if flowError}<p class="error" role="alert">{flowError}</p>{/if}

		<h2>Pull request</h2>
		{#if pr}
			<p class="pr" data-testid="pull-request">
				{#if pr.url}<a href={pr.url} rel="external noopener" target="_blank"
						>{pr.number ? `#${pr.number}` : pr.head}</a
					>{:else}<span class="mono">{pr.head}</span>{/if}
				<span class="badge" data-state={pr.state}>{pr.state}</span>
				<span class="badge">checks: {pr.checks}</span>
				<span class="badge">review: {pr.review.replace('_', ' ')}</span>
				<span class="muted small">{pr.head} → {pr.base}</span>
			</p>
		{:else}
			<p class="muted">No pull request yet.</p>
		{/if}
		{#if canWrite}
			<div class="actions">
				{#if !pr}<button disabled={prBusy} onclick={() => prAction('open')}
						>Open pull request</button
					>{/if}
				{#if pr}<button disabled={prBusy} onclick={() => prAction('refresh')}>Refresh</button>{/if}
				{#if pr?.state === 'open' && pr.forge !== 'git'}
					<button class="primary" disabled={prBusy} onclick={() => prAction('merge')}>Merge</button>
				{/if}
			</div>
		{/if}
		{#if prError}<p class="error" role="alert">{prError}</p>{/if}
	{/if}

	{#if runs.length || reports.length || questions.length}
		<h2>Agent activity</h2>
		{#if questions.some((q) => q.status === 'open')}
			<ul class="activity" aria-label="Open questions">
				{#each questions.filter((q) => q.status === 'open') as q (q.id)}
					<li class="question" class:blocking={q.blocking}>
						<strong>{q.blocking ? 'Blocking question' : 'Question'}:</strong>
						{q.text}
						{#if q.context}<div class="muted small">{q.context}</div>{/if}
					</li>
				{/each}
			</ul>
		{/if}
		{#if runs.length}
			<SessionTimeline {runs} {reports} {questions} {history} {usage} />
		{/if}
		{#if reports.length}
			<ul class="activity" aria-label="Agent reports">
				{#each [...reports].reverse() as r (r.id)}
					<li>
						<span class="badge">{r.kind.replace('_', ' ')}{r.outcome ? `: ${r.outcome}` : ''}</span>
						{r.text}
						{#if r.review}<span class="badge" title={r.review_comment}>{r.review}</span>{/if}
						{#if r.detail}<details>
								<summary class="muted small">Details</summary>
								<div class="markdown">{r.detail}</div>
							</details>{/if}
						<span class="muted small">{new Date(r.created_at).toLocaleString()}</span>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}

	<h2>History</h2>
	<ol class="history">
		{#each history as e (e.seq)}
			<li>
				<span class="muted">{new Date(e.occurred_at).toLocaleString()}</span>
				<span>{e.type}</span>
				<span class="muted">{e.actor.kind === 'human' ? e.actor.subject : e.actor.kind}</span>
			</li>
		{/each}
	</ol>
{/if}

<style>
	.stages {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
		padding: 0;
		list-style: none;
	}
	.stages li {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.2rem 0.6rem;
	}
	.stages li.current {
		border-color: var(--accent);
		font-weight: 600;
	}
	.pr {
		display: flex;
		gap: 0.5rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
	.activity {
		list-style: none;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}
	.activity .question {
		border-left: 3px solid var(--warn);
		padding-left: 0.5rem;
	}
	.activity .question.blocking {
		border-left-color: var(--danger);
	}
	.small {
		font-size: 0.85rem;
	}
	.crumbs {
		margin-bottom: 0.75rem;
		font-size: 0.9rem;
	}
	.title {
		display: flex;
		gap: 0.75rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.title h1 {
		margin: 0;
	}
	.meta {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
		align-items: center;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
		margin: 0.75rem 0;
	}
	.description {
		white-space: pre-wrap;
	}
	.edit {
		display: grid;
		gap: 0.75rem;
	}
	.edit > label {
		display: grid;
		gap: 0.25rem;
		font-size: 0.85rem;
		color: var(--muted);
	}
	.deps,
	.history {
		padding: 0;
		list-style: none;
		display: grid;
		gap: 0.4rem;
	}
	.deps li,
	.history li {
		display: flex;
		gap: 0.75rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.dep-type {
		min-width: 6rem;
		color: var(--muted);
	}
	.history li {
		font-size: 0.9rem;
	}
</style>
