<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	const project = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let organization = $state<string>();
	let sessions = $state<Schemas['PlannerSession'][]>([]);
	let loaded = $state(false);
	let error = $state<string>();
	let title = $state('');
	let formError = $state<string>();

	const canPlan = $derived(!!session?.permissions.can('tracker.write', { organization, project }));

	async function load(s: Session, key: string) {
		const path = { params: { path: { project: key } } };
		const [p, list] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', path),
			s.api.GET('/api/v1/projects/{project}/planner/sessions', path)
		]);
		if (!list.data) {
			error = apiError(list.error);
			return;
		}
		organization = p.data?.organization;
		sessions = list.data.items;
		loaded = true;
	}

	$effect(() => {
		const key = project;
		getSession()
			.then((s) => {
				session = s;
				return load(s, key);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		formError = undefined;
		const { data, error: err } = await session.api.POST(
			'/api/v1/projects/{project}/planner/sessions',
			{ params: { path: { project } }, body: { title } }
		);
		if (!data) {
			formError = apiError(err);
			return;
		}
		await goto(resolve('/planner/[session]', { session: data.id }));
	}
</script>

<svelte:head><title>Planner · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Planner</h1>
<p class="muted">
	Plan with the planner agent: it researches, writes knowledge and proposes changesets that you
	approve.
</p>

{#if canPlan}
	<form class="form card" onsubmit={create} aria-label="New chat">
		<label class="grow"
			>Topic <input bind:value={title} required placeholder="Authentication" /></label
		>
		<button class="primary" type="submit">Start chat</button>
		{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	</form>
{/if}

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if sessions.length === 0}
	<p class="muted">No chats yet.</p>
{:else}
	<ul class="sessions" aria-label="Chats">
		{#each sessions as s (s.id)}
			<li class="card">
				<a href={resolve('/planner/[session]', { session: s.id })}>{s.title}</a>
				{#if s.running}<span class="badge">answering</span>{/if}
				<span class="muted small">{new Date(s.updated_at).toLocaleString()}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.grow {
		flex: 1;
		min-width: 12rem;
	}
	.sessions {
		list-style: none;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.sessions li {
		display: flex;
		gap: 0.5rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.small {
		font-size: 0.85rem;
		margin-left: auto;
	}
</style>
