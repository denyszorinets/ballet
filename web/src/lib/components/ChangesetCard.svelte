<script lang="ts">
	import { resolve } from '$app/paths';
	import { apiError } from '$lib/api/client';
	import { describe, toggle, type Changeset } from '$lib/changesets';
	import { renderMarkdown } from '$lib/markdown';
	import { getSession } from '$lib/session';

	let {
		id,
		canDecide,
		refresh = 0
	}: {
		id: string;
		/** The viewer may approve or reject (tracker.write). */
		canDecide: boolean;
		/** Changing it reloads the changeset (e.g. after a project event). */
		refresh?: number;
	} = $props();

	let cs = $state<Changeset>();
	let selected = $state(new Set<number>());
	let error = $state<string>();
	let busy = $state(false);

	const proposed = $derived(cs?.status === 'proposed');

	async function load(changeset: string) {
		const s = await getSession();
		const { data, error: err } = await s.api.GET('/api/v1/changesets/{changeset}', {
			params: { path: { changeset } }
		});
		if (!data) {
			error = apiError(err);
			return;
		}
		if (!cs || cs.id !== data.id || cs.status !== data.status) {
			selected = new Set(data.operations.map((_, i) => i));
		}
		cs = data;
	}

	$effect(() => {
		void refresh;
		void load(id);
	});

	async function decide(operations: number[] | 'reject') {
		busy = true;
		error = undefined;
		try {
			const s = await getSession();
			const path = { params: { path: { changeset: id } } };
			const res =
				operations === 'reject'
					? await s.api.POST('/api/v1/changesets/{changeset}/reject', path)
					: await s.api.POST('/api/v1/changesets/{changeset}/apply', {
							...path,
							body: { operations }
						});
			if (res.data) cs = res.data;
			else error = apiError(res.error);
		} finally {
			busy = false;
		}
	}
</script>

{#if cs}
	<article class="changeset card" aria-label={`Changeset ${cs.title}`}>
		<header>
			<strong>{cs.title}</strong>
			<span class="badge" data-testid="changeset-status">{cs.status}</span>
			<span class="muted small">{cs.operations.length} operations</span>
		</header>
		{#if cs.summary}
			<div class="markdown summary">
				<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
				{@html renderMarkdown(cs.summary)}
			</div>
		{/if}
		<ol class="ops">
			{#each cs.operations as op, i (i)}
				{@const result = cs.results[i]}
				<li>
					{#if proposed && canDecide}
						<label>
							<input
								type="checkbox"
								checked={selected.has(i)}
								disabled={busy}
								onchange={() => cs && (selected = toggle(selected, i, cs.operations))}
							/>
							{describe(op, cs.operations)}
						</label>
					{:else}
						<span class:muted={cs.status === 'applied' && !cs.approved.includes(i)}
							>{describe(op, cs.operations)}</span
						>
						{#if result?.key && op.kind.endsWith('feature')}
							<span class="mono">{result.key}</span>
						{:else if result?.key}
							<a class="mono" href={resolve('/items/[item]', { item: result.key })}>{result.key}</a>
						{:else if cs.status === 'applied' && !cs.approved.includes(i)}
							<span class="muted small">not applied</span>
						{/if}
					{/if}
				</li>
			{/each}
		</ol>
		{#if proposed && canDecide}
			<div class="actions">
				<button
					class="primary"
					disabled={busy || selected.size === 0}
					onclick={() => decide([...selected].sort((a, b) => a - b))}
					>Approve selected ({selected.size})</button
				>
				<button disabled={busy} onclick={() => cs && decide(cs.operations.map((_, i) => i))}
					>Approve all</button
				>
				<button disabled={busy} onclick={() => decide('reject')}>Reject</button>
			</div>
		{/if}
		{#if error}<p class="error" role="alert">{error}</p>{/if}
	</article>
{:else if error}
	<p class="error" role="alert">{error}</p>
{/if}

<style>
	.changeset {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	header {
		display: flex;
		gap: 0.5rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.ops {
		margin: 0;
		padding-left: 1.25rem;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.ops li {
		display: flex;
		gap: 0.5rem;
		align-items: baseline;
		flex-wrap: wrap;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
		flex-wrap: wrap;
	}
	.small {
		font-size: 0.85rem;
	}
	.summary :global(p) {
		margin: 0.25rem 0;
	}
</style>
