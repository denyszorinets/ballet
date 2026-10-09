<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	let {
		project,
		customer
	}: {
		/** The project; absent: the whole platform. */
		project?: string;
		customer?: string;
	} = $props();

	let session = $state<Session>();
	let pause = $state<Schemas['Pause']>();
	/** A pause of everything, shown on project pages too. */
	let platformPause = $state<Schemas['Pause']>();
	let reason = $state('');
	let error = $state<string>();
	let notice = $state<string>();
	let busy = $state(false);

	const canManage = $derived(
		!!session?.permissions.can('run.manage', project ? { customer, project } : {})
	);
	const scopeName = $derived(project ? `project ${project}` : 'the platform');

	async function load(s: Session) {
		const { data, error: err } = await s.api.GET('/api/v1/pauses');
		if (!data) {
			error = apiError(err);
			return;
		}
		platformPause = data.items.find((p) => p.scope === 'platform');
		pause = project ? data.items.find((p) => p.project === project) : platformPause;
	}

	$effect(() => {
		void project;
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function act(kind: 'pause' | 'resume' | 'kill') {
		if (!session) return;
		if (kind === 'kill' && !confirm(`Cancel every run in progress in ${scopeName} and pause it?`))
			return;
		busy = true;
		error = notice = undefined;
		const body = { reason };
		let res: { error?: Schemas['Error']; data?: unknown; response: Response };
		if (project) {
			const path = { params: { path: { project } } };
			res =
				kind === 'pause'
					? await session.api.PUT('/api/v1/projects/{project}/pause', { ...path, body })
					: kind === 'kill'
						? await session.api.POST('/api/v1/projects/{project}/kill', { ...path, body })
						: await session.api.DELETE('/api/v1/projects/{project}/pause', path);
		} else {
			res =
				kind === 'pause'
					? await session.api.PUT('/api/v1/pause', { body })
					: kind === 'kill'
						? await session.api.POST('/api/v1/kill', { body })
						: await session.api.DELETE('/api/v1/pause');
		}
		busy = false;
		if (!res.response.ok) {
			error = apiError(res.error);
			return;
		}
		if (kind === 'kill') {
			const n = (res.data as Schemas['KillResult']).cancelled;
			notice = `Cancelled ${n} run${n === 1 ? '' : 's'}.`;
		}
		reason = '';
		await load(session);
	}
</script>

<section class="control" class:paused={!!pause} aria-label="Autonomous work">
	{#if pause}
		<p role="status">
			<strong>Paused</strong> — autonomous work in {scopeName} is stopped
			{#if pause.reason}: {pause.reason}{/if}
			<span class="muted small"
				>(by {pause.paused_by}, {new Date(pause.paused_at).toLocaleString()})</span
			>
		</p>
	{:else if project && platformPause}
		<p role="status"><strong>Paused</strong> — all autonomous work is paused.</p>
	{:else}
		<p class="muted small" role="status">Autonomous work in {scopeName} is running.</p>
	{/if}
	{#if canManage}
		<div class="actions">
			{#if !pause}
				<input bind:value={reason} placeholder="Reason (optional)" aria-label="Reason" />
				<button onclick={() => act('pause')} disabled={busy}>Pause</button>
			{:else}
				<button class="primary" onclick={() => act('resume')} disabled={busy}>Resume</button>
			{/if}
			<button class="danger" onclick={() => act('kill')} disabled={busy}>Stop all runs</button>
		</div>
	{/if}
	{#if notice}<p class="muted small">{notice}</p>{/if}
	{#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
	.control {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 0.5rem 0.75rem;
		margin: 0.75rem 0;
	}
	.control.paused {
		border-color: var(--danger);
	}
	.control p {
		margin: 0.25rem 0;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
		align-items: center;
	}
	.small {
		font-size: 0.85rem;
	}
	button.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
</style>
