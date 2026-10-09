<script lang="ts">
	import WorkControl from '$lib/components/WorkControl.svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import type { StreamEvent } from '$lib/realtime/client';
	import { getSession, type Session } from '$lib/session';
	import { boardColumns, stateLabel, upsert, type Item } from '$lib/tracker';

	const key = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let project = $state<Schemas['Project']>();
	let items = $state<Item[]>([]);
	let runnable = $state<Set<string>>(new Set());
	let error = $state<string>();
	let form = $state({ title: '', type: 'feature' as Schemas['TicketType'] });
	let formError = $state<string>();

	const tickets = $derived(items.filter((i) => i.kind === 'ticket'));
	const containers = $derived(items.filter((i) => i.kind !== 'ticket'));
	const canWrite = $derived(
		!!project &&
			!!session?.permissions.can('tracker.write', {
				organization: project.organization,
				project: key
			})
	);

	async function loadAll(s: Session, k: string) {
		const path = { params: { path: { project: k } } };
		const [p, list] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', path),
			s.api.GET('/api/v1/projects/{project}/items', path)
		]);
		if (!p.data || !list.data) {
			error = apiError(p.error ?? list.error);
			return;
		}
		project = p.data;
		items = list.data.items;
		await loadRunnable(s, k);
	}

	async function loadRunnable(s: Session, k: string) {
		const { data } = await s.api.GET('/api/v1/projects/{project}/runnable', {
			params: { path: { project: k } }
		});
		if (data) runnable = new Set(data.items.map((i) => i.key));
	}

	async function onEvent(s: Session, k: string, e: StreamEvent) {
		if (e.entity_type === 'item' && e.entity_key) {
			const { data } = await s.api.GET('/api/v1/items/{item}', {
				params: { path: { item: e.entity_key } }
			});
			if (data) items = upsert(items, data);
		}
		await loadRunnable(s, k);
	}

	$effect(() => {
		const k = key;
		let sub: { unsubscribe(): void } | undefined;
		let cancelled = false;
		getSession()
			.then(async (s) => {
				session = s;
				// Subscribe first, then load: events after the snapshot are applied on top.
				sub = s.realtime.subscribe(`project:${k}`, {
					onEvent: (e) => void onEvent(s, k, e),
					onResync: () => void loadAll(s, k)
				});
				if (cancelled) sub.unsubscribe();
				await loadAll(s, k);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => {
			cancelled = true;
			sub?.unsubscribe();
		};
	});

	async function createTicket(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		formError = undefined;
		const { data, error: err } = await session.api.POST('/api/v1/projects/{project}/items', {
			params: { path: { project: key } },
			body: { kind: 'ticket', title: form.title, type: form.type }
		});
		if (err) {
			formError = apiError(err);
			return;
		}
		items = upsert(items, data);
		form = { ...form, title: '' };
	}
</script>

<svelte:head><title>{key} board · Ballet</title></svelte:head>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !project}
	<p class="muted">Loading…</p>
{:else}
	<div class="title">
		<h1>{project.name}</h1>
		<span class="mono muted">{project.key}</span>
		<a href={resolve('/organizations/[organization]', { organization: project.organization })}
			>{project.organization}</a
		>
		<a href={resolve('/projects/[project]/planner', { project: project.key })}>Planner</a>
		<a href={resolve('/projects/[project]/changesets', { project: project.key })}>Changesets</a>
		<a href={resolve('/projects/[project]/assumptions', { project: project.key })}>Assumptions</a>
		<a href={resolve('/projects/[project]/pipelines', { project: project.key })}>Pipelines</a>
		<a href={resolve('/projects/[project]/digest', { project: project.key })}>Digest</a>
		<a href={resolve('/projects/[project]/skills', { project: project.key })}>Skills</a>
		<a href={resolve('/projects/[project]/settings', { project: project.key })}>Settings</a>
	</div>
	<WorkControl project={project.key} organization={project.organization} />

	{#if canWrite}
		<form class="form" onsubmit={createTicket} aria-label="Add ticket">
			<label class="grow"
				>New ticket <input bind:value={form.title} required placeholder="Title" /></label
			>
			<label
				>Type
				<select bind:value={form.type}>
					<option value="feature">feature</option>
					<option value="bug">bug</option>
					<option value="tech_debt">tech_debt</option>
					<option value="docs">docs</option>
					<option value="spike">spike</option>
				</select>
			</label>
			<button class="primary" type="submit">Add</button>
			{#if formError}<p class="error" role="alert">{formError}</p>{/if}
		</form>
	{/if}

	<div class="board" role="list" aria-label="Board">
		{#each boardColumns as state (state)}
			{@const column = tickets.filter((t) => t.state === state)}
			<section class="column" role="listitem" aria-label={stateLabel[state]}>
				<h2>{stateLabel[state]} <span class="muted count">{column.length}</span></h2>
				{#each column as t (t.key)}
					<a class="card ticket" href={resolve('/items/[item]', { item: t.key })}>
						<span class="mono muted">{t.key}</span>
						<span class="ticket-title">{t.title}</span>
						<span class="meta">
							<span class="badge">{t.type}</span>
							{#if t.stage}<span class="badge">{t.stage}</span>{/if}
							{#if t.state === 'ready' && !runnable.has(t.key)}
								<span class="badge blocked">Blocked</span>
							{/if}
						</span>
					</a>
				{/each}
			</section>
		{/each}
	</div>

	{#if containers.length > 0}
		<h2>Epics and milestones</h2>
		<ul class="containers">
			{#each containers as c (c.key)}
				<li>
					<a href={resolve('/items/[item]', { item: c.key })}>
						<span class="mono muted">{c.key}</span>
						{c.title}
					</a>
					<span class="badge">{c.kind}</span>
					<span class="muted">{stateLabel[c.state]}</span>
				</li>
			{/each}
		</ul>
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
		margin: 0 0 1rem;
	}
	.form {
		margin-bottom: 1rem;
	}
	.grow {
		flex: 1;
		min-width: 14rem;
	}
	.board {
		display: grid;
		grid-template-columns: repeat(6, minmax(11rem, 1fr));
		gap: 0.75rem;
		overflow-x: auto;
		padding-bottom: 0.5rem;
	}
	.column {
		background: color-mix(in srgb, var(--surface) 60%, var(--bg));
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.5rem;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		min-height: 8rem;
	}
	.column h2 {
		font-size: 0.85rem;
		margin: 0.25rem 0.25rem 0.25rem;
	}
	.count {
		font-weight: 400;
	}
	.ticket {
		display: grid;
		gap: 0.25rem;
		padding: 0.6rem;
		color: var(--text);
		text-decoration: none;
		font-size: 0.9rem;
	}
	.ticket:hover {
		border-color: var(--accent);
	}
	.ticket-title {
		overflow-wrap: anywhere;
	}
	.meta {
		display: flex;
		gap: 0.25rem;
		flex-wrap: wrap;
	}
	.blocked {
		color: var(--warn);
		border-color: var(--warn);
	}
	.containers {
		list-style: none;
		padding: 0;
		display: grid;
		gap: 0.4rem;
	}
	.containers li {
		display: flex;
		gap: 0.75rem;
		align-items: baseline;
	}
</style>
