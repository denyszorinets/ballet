<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';
	import { formatScope, parseScope, scopeTarget, type Scope } from '$lib/skills';

	const scopeParam = $derived(page.url.searchParams.get('scope'));
	/** Without ?scope: the organization, or the caller's first customer or project binding. */
	let defaultScope = $state<string>();
	const scope = $derived(parseScope(scopeParam ?? defaultScope ?? 'organization'));

	let session = $state<Session>();
	let skills = $state<Schemas['Skill'][]>([]);
	let projectCustomer = $state<string>();
	let loaded = $state(false);
	let error = $state<string>();

	let pick = $state<Scope>({ kind: 'organization', key: '' });
	let form = $state({ name: '', description: '' });
	let formError = $state<string>();

	const canWrite = $derived(
		!!session?.permissions.can('skill.write', scopeTarget(scope, projectCustomer))
	);

	async function load(s: Session, sc: Scope) {
		error = undefined;
		loaded = false;
		projectCustomer = undefined;
		if (sc.kind === 'project') {
			const p = await s.api.GET('/api/v1/projects/{project}', {
				params: { path: { project: sc.key } }
			});
			projectCustomer = p.data?.customer;
		}
		const res = await s.api.GET('/api/v1/skills', {
			params: { query: { scope: formatScope(sc) } }
		});
		if (!res.data) {
			error = apiError(res.error);
			return;
		}
		skills = res.data.items;
		loaded = true;
	}

	$effect(() => {
		const sc = scope;
		const explicit = scopeParam !== null || defaultScope !== undefined;
		pick = { ...sc };
		getSession()
			.then(async (s) => {
				session = s;
				if (!explicit) {
					if (!s.permissions.loaded) await s.permissions.load(s.api);
					defaultScope = s.permissions.can('skill.read')
						? 'organization'
						: (s.permissions.bindings.find((b) => b.scope !== 'organization')?.scope ??
							'organization');
					return; // the effect reruns with the default scope
				}
				return load(s, sc);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	function show(e: SubmitEvent) {
		e.preventDefault();
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- resolved path plus a query
		void goto(`${resolve('/skills')}?scope=${encodeURIComponent(formatScope(pick))}`);
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		formError = undefined;
		const { data, error: err } = await session.api.POST('/api/v1/skills', {
			body: { scope: formatScope(scope), name: form.name, description: form.description }
		});
		if (!data) {
			formError = apiError(err);
			return;
		}
		await goto(resolve('/skills/[skill]', { skill: data.id }));
	}
</script>

<svelte:head><title>Skills · Ballet</title></svelte:head>

<h1>Skills</h1>

<form class="form" onsubmit={show} aria-label="Choose scope">
	<label
		>Scope
		<select bind:value={pick.kind}>
			<option value="organization">Organization</option>
			<option value="customer">Customer</option>
			<option value="project">Project</option>
		</select>
	</label>
	{#if pick.kind !== 'organization'}
		<label
			>{pick.kind === 'customer' ? 'Customer key' : 'Project key'}
			<input bind:value={pick.key} required /></label
		>
	{/if}
	<button type="submit">Show</button>
</form>

<h2 class="mono">{formatScope(scope)}</h2>
{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if skills.length === 0}
	<p class="muted">No skills at this scope.</p>
{:else}
	<div class="table-wrap">
		<table>
			<thead><tr><th>Name</th><th>Description</th><th>Latest</th></tr></thead>
			<tbody>
				{#each skills as s (s.id)}
					<tr>
						<td class="mono"><a href={resolve('/skills/[skill]', { skill: s.id })}>{s.name}</a></td>
						<td>{s.description}</td>
						<td>{s.latest_version ? `v${s.latest_version}` : 'unpublished'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}

{#if canWrite && loaded}
	<h2>New skill</h2>
	<form class="form card" onsubmit={create} aria-label="New skill">
		<label
			>Name <input
				bind:value={form.name}
				required
				placeholder="code-review"
				pattern="[a-z][a-z0-9]*(-[a-z0-9]+)*"
			/></label
		>
		<label class="grow">Description <input bind:value={form.description} required /></label>
		<button class="primary" type="submit">Create skill</button>
		{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	</form>
{/if}

<style>
	.grow {
		flex: 1;
		min-width: 12rem;
	}
</style>
