<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	type Resolved = Schemas['ResolvedSkill'];
	type Pin = Schemas['ProjectSkillPin'];

	interface Row {
		name: string;
		resolved?: Resolved;
		pin?: Pin;
	}

	const project = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let customer = $state<string>();
	let rows = $state<Row[]>([]);
	let loaded = $state(false);
	let error = $state<string>();
	let actionError = $state<string>();

	const canWrite = $derived(!!session?.permissions.can('skill.write', { customer, project }));

	async function load(s: Session, key: string) {
		const path = { params: { path: { project: key } } };
		const [p, r, pins] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', path),
			s.api.GET('/api/v1/projects/{project}/skills', path),
			s.api.GET('/api/v1/projects/{project}/skill-pins', path)
		]);
		if (!r.data) {
			error = apiError(r.error);
			return;
		}
		customer = p.data?.customer;
		const byName: Record<string, Row> = {};
		for (const x of r.data.items) byName[x.name] = { name: x.name, resolved: x };
		for (const x of pins.data?.items ?? [])
			byName[x.name] = { ...byName[x.name], name: x.name, pin: x };
		rows = Object.values(byName).sort((a, b) => a.name.localeCompare(b.name));
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

	/** The select value for a row: latest, v<N> or disabled. */
	function choice(r: Row): string {
		if (r.pin?.disabled) return 'disabled';
		if (r.pin && r.pin.version > 0) return `v${r.pin.version}`;
		return 'latest';
	}

	function options(r: Row): number[] {
		const n = Math.max(r.resolved?.latest_version ?? 0, r.pin?.version ?? 0);
		return Array.from({ length: n }, (_, i) => n - i);
	}

	async function change(r: Row, value: string) {
		if (!session) return;
		actionError = undefined;
		const params = { params: { path: { project, name: r.name } } };
		const res =
			value === 'latest'
				? r.pin
					? await session.api.DELETE('/api/v1/projects/{project}/skills/{name}/pin', params)
					: undefined
				: await session.api.PUT('/api/v1/projects/{project}/skills/{name}/pin', {
						...params,
						body: value === 'disabled' ? { disabled: true } : { version: Number(value.slice(1)) }
					});
		if (res?.error) actionError = apiError(res.error);
		await load(session, project);
	}
</script>

<svelte:head><title>Skills · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Project skills</h1>
<p class="muted">
	Published skills of the platform, the customer and the project; the most specific scope wins
	per name. Pin a version or disable a skill for this project.
</p>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !loaded}
	<p class="muted">Loading…</p>
{:else if rows.length === 0}
	<p class="muted">No published skills apply to this project.</p>
{:else}
	{#if actionError}<p class="error" role="alert">{actionError}</p>{/if}
	<div class="table-wrap">
		<table>
			<thead><tr><th>Skill</th><th>Scope</th><th>Uses</th><th>Latest</th></tr></thead>
			<tbody>
				{#each rows as r (r.name)}
					<tr>
						<td class="mono">
							{#if r.resolved?.skill_id}
								<a href={resolve('/skills/[skill]', { skill: r.resolved.skill_id })}>{r.name}</a>
							{:else}
								{r.name}
							{/if}
						</td>
						<td class="mono muted">{r.resolved?.scope ?? ''}</td>
						<td>
							{#if canWrite}
								<select
									aria-label={`Version of ${r.name}`}
									value={choice(r)}
									onchange={(e) => change(r, e.currentTarget.value)}
								>
									<option value="latest">Latest</option>
									{#each options(r) as n (n)}<option value={`v${n}`}>v{n}</option>{/each}
									<option value="disabled">Disabled</option>
								</select>
							{:else if r.pin?.disabled}
								Disabled
							{:else if r.resolved?.version}
								v{r.resolved.version}{r.resolved.pinned ? ' (pinned)' : ''}
							{/if}
							{#if r.resolved?.problem}
								<p class="error small">{r.resolved.problem}</p>
							{/if}
						</td>
						<td>{r.resolved?.latest_version ? `v${r.resolved.latest_version}` : ''}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}

<style>
	.small {
		font-size: 0.85rem;
		margin: 0.25rem 0 0;
	}
</style>
