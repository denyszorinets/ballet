<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	let {
		customer,
		project
	}: {
		customer: string;
		/** The project; absent: the customer's budget. */
		project?: string;
	} = $props();

	let session = $state<Session>();
	let budget = $state<Schemas['Budget']>();
	let ticketTokens = $state(0);
	let dailyTokens = $state(0);
	let error = $state<string>();
	let saved = $state(false);

	const canEdit = $derived(
		!!session?.permissions.can(project ? 'project.update' : 'customer.update', {
			customer,
			project
		})
	);
	const fmt = (n: number) => n.toLocaleString();
	const exhausted = $derived(
		!!budget && budget.daily_tokens > 0 && budget.used_today >= budget.daily_tokens
	);

	async function load(s: Session) {
		const res = project
			? await s.api.GET('/api/v1/projects/{project}/budget', { params: { path: { project } } })
			: await s.api.GET('/api/v1/customers/{customer}/budget', { params: { path: { customer } } });
		if (!res.data) {
			error = apiError(res.error);
			return;
		}
		budget = res.data;
		ticketTokens = res.data.ticket_tokens;
		dailyTokens = res.data.daily_tokens;
	}

	$effect(() => {
		void project;
		void customer;
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!session || !budget) return;
		error = undefined;
		saved = false;
		const body = {
			ticket_tokens: ticketTokens,
			daily_tokens: dailyTokens,
			version: budget.version
		};
		const res = project
			? await session.api.PUT('/api/v1/projects/{project}/budget', {
					params: { path: { project } },
					body
				})
			: await session.api.PUT('/api/v1/customers/{customer}/budget', {
					params: { path: { customer } },
					body
				});
		if (!res.data) {
			error = apiError(res.error);
			return;
		}
		budget = res.data;
		saved = true;
	}
</script>

<section aria-label="Budget">
	<h2>Budget</h2>
	<p class="muted">
		Tokens unattended work may use (input, output and cache writes; 0: no limit). Work over budget
		waits: a used-up ticket budget asks the humans, a daily budget waits for the next day (UTC).
		{#if project}The ticket budget overrides the customer's.{/if}
	</p>
	{#if budget}
		<p role="status" class:exhausted>
			Used today: <strong>{fmt(budget.used_today)}</strong>
			{#if budget.daily_tokens > 0}of {fmt(budget.daily_tokens)} tokens{#if exhausted}
					— the daily budget is used up; new work waits{/if}{:else}tokens (no daily limit){/if}
		</p>
		<form class="form card" onsubmit={save} aria-label="Budget limits">
			<label
				>Per ticket <input
					type="number"
					min="0"
					step="1000"
					bind:value={ticketTokens}
					disabled={!canEdit}
				/></label
			>
			<label
				>Per day <input
					type="number"
					min="0"
					step="1000"
					bind:value={dailyTokens}
					disabled={!canEdit}
				/></label
			>
			{#if canEdit}<button class="primary" type="submit">Save budget</button>{/if}
		</form>
		{#if saved}<p class="ok">Saved.</p>{/if}
	{/if}
	{#if error}<p class="error" role="alert">{error}</p>{/if}
</section>

<style>
	.exhausted {
		color: var(--danger);
	}
	.ok {
		color: var(--ok);
	}
</style>
