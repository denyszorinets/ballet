<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	type Review = 'open' | 'confirmed' | 'rejected' | '';
	type Assumption = Schemas['Report'];

	const project = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let organization = $state<string>();
	let items = $state<Assumption[]>([]);
	let review = $state<Review>('open');
	let loaded = $state(false);
	let error = $state<string>();
	/** The assumption being rejected, and the comment typed for it. */
	let rejecting = $state<string>();
	let comment = $state('');
	let actionError = $state<string>();

	const canReview = $derived(
		!!session?.permissions.can('tracker.write', { organization, project })
	);

	async function load(s: Session, key: string, r: Review) {
		const [p, list] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', { params: { path: { project: key } } }),
			s.api.GET('/api/v1/projects/{project}/assumptions', {
				params: { path: { project: key }, query: r ? { review: r } : {} }
			})
		]);
		if (!list.data) {
			error = apiError(list.error);
			return;
		}
		organization = p.data?.organization;
		items = list.data.items;
		loaded = true;
	}

	$effect(() => {
		const key = project;
		const r = review;
		let sub: { unsubscribe(): void } | undefined;
		getSession()
			.then((s) => {
				session = s;
				sub = s.realtime.subscribe(`project:${key}`, {
					onEvent: (e) => {
						if (e.type === 'item.report_added' || e.type === 'item.assumption_reviewed')
							void load(s, key, r);
					},
					onResync: () => void load(s, key, r)
				});
				return load(s, key, r);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
		return () => sub?.unsubscribe();
	});

	async function decide(a: Assumption, confirm: boolean) {
		if (!session) return;
		actionError = undefined;
		const path = { params: { path: { report: a.id } }, body: { comment: confirm ? '' : comment } };
		const res = confirm
			? await session.api.POST('/api/v1/assumptions/{report}/confirm', path)
			: await session.api.POST('/api/v1/assumptions/{report}/reject', path);
		if (!res.data) {
			actionError = apiError(res.error);
			return;
		}
		rejecting = undefined;
		comment = '';
		await load(session, project, review);
	}
</script>

<svelte:head><title>Assumptions · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Assumptions</h1>
<p class="muted">
	Reversible decisions agents took without asking. Confirm them, or reject them with what is right:
	a rejection reaches the ticket’s next sessions, or proposes a correction ticket once the ticket is
	done.
</p>

<label
	>Review
	<select bind:value={review}>
		<option value="open">Not reviewed</option>
		<option value="confirmed">Confirmed</option>
		<option value="rejected">Rejected</option>
		<option value="">All</option>
	</select>
</label>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if items.length === 0}
	<p class="muted">No assumptions.</p>
{:else}
	<ul class="list" aria-label="Assumptions">
		{#each items as a (a.id)}
			<li class="card">
				<p class="muted small">
					<a href={resolve('/items/[item]', { item: a.ticket ?? '' })}
						>{a.ticket} · {a.ticket_title}</a
					>
					· {new Date(a.created_at).toLocaleString()}
				</p>
				<p class="text">{a.text}</p>
				{#if a.detail}<p class="muted">{a.detail}</p>{/if}

				{#if a.review}
					<p class="review">
						<span class="badge" class:danger={a.review === 'rejected'}>{a.review}</span>
						<span class="muted small">by {a.reviewed_by}</span>
						{#if a.review_comment}— {a.review_comment}{/if}
						{#if a.follow_up?.startsWith('changeset:')}
							<a href={resolve('/projects/[project]/changesets', { project })}
								>correction proposed as a changeset</a
							>
						{:else if a.follow_up}
							<span class="muted small">sent to the ticket’s next sessions</span>
						{/if}
					</p>
				{:else if canReview}
					{#if rejecting === a.id}
						<form
							onsubmit={(e) => {
								e.preventDefault();
								void decide(a, false);
							}}
						>
							<label
								>What is right instead?
								<textarea bind:value={comment} rows="3"></textarea>
							</label>
							<div class="actions">
								<button class="primary" type="submit" disabled={!comment.trim()}>Reject</button>
								<button type="button" onclick={() => (rejecting = undefined)}>Cancel</button>
							</div>
						</form>
					{:else}
						<div class="actions">
							<button onclick={() => decide(a, true)}>Confirm</button>
							<button
								onclick={() => {
									rejecting = a.id;
									comment = '';
								}}>Reject…</button
							>
						</div>
					{/if}
				{/if}
			</li>
		{/each}
	</ul>
	{#if actionError}<p class="error" role="alert">{actionError}</p>{/if}
{/if}

<style>
	.list {
		list-style: none;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		margin-top: 1rem;
	}
	.text {
		font-weight: 600;
		margin: 0.25rem 0;
	}
	.small {
		font-size: 0.85rem;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		margin-top: 0.5rem;
	}
	.badge.danger {
		border-color: var(--danger);
		color: var(--danger);
	}
	textarea {
		width: 100%;
		box-sizing: border-box;
		font: inherit;
	}
</style>
