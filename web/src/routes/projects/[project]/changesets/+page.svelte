<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import ChangesetCard from '$lib/components/ChangesetCard.svelte';
	import { getSession, type Session } from '$lib/session';

	type Status = Schemas['ChangesetStatus'];

	const project = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let organization = $state<string>();
	let ids = $state<string[]>([]);
	let status = $state<Status | ''>('proposed');
	let loaded = $state(false);
	let error = $state<string>();
	let refresh = $state(0);

	const canDecide = $derived(
		!!session?.permissions.can('tracker.write', { organization, project })
	);

	async function load(s: Session, key: string, st: Status | '') {
		const [p, list] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', { params: { path: { project: key } } }),
			s.api.GET('/api/v1/projects/{project}/changesets', {
				params: { path: { project: key }, query: st ? { status: st } : {} }
			})
		]);
		if (!list.data) {
			error = apiError(list.error);
			return;
		}
		organization = p.data?.organization;
		ids = list.data.items.map((c) => c.id);
		loaded = true;
	}

	$effect(() => {
		const key = project;
		const st = status;
		let sub: { unsubscribe(): void } | undefined;
		getSession()
			.then((s) => {
				session = s;
				sub = s.realtime.subscribe(`project:${key}`, {
					onEvent: (e) => {
						if (e.entity_type === 'changeset') {
							refresh++;
							void load(s, key, st);
						}
					},
					onResync: () => void load(s, key, st)
				});
				return load(s, key, st);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => sub?.unsubscribe();
	});
</script>

<svelte:head><title>Changesets · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Changesets</h1>

<label
	>Status
	<select bind:value={status}>
		<option value="proposed">Proposed</option>
		<option value="applied">Applied</option>
		<option value="rejected">Rejected</option>
		<option value="">All</option>
	</select>
</label>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if ids.length === 0}
	<p class="muted">No changesets.</p>
{:else}
	<div class="list">
		{#each ids as id (id)}
			<ChangesetCard {id} {canDecide} {refresh} />
		{/each}
	</div>
{/if}

<style>
	.list {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		margin-top: 1rem;
	}
</style>
