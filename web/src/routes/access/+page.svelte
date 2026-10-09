<script lang="ts">
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	type Kind = 'platform' | 'customer' | 'project';

	let session = $state<Session>();
	let bindings = $state<Schemas['RoleBinding'][]>();
	let roles = $state<string[]>([]);
	let error = $state<string>();
	let form = $state({
		claim: 'groups',
		value: '',
		role: 'engineer',
		kind: 'customer' as Kind,
		key: ''
	});
	let formError = $state<string>();

	async function load(s: Session) {
		const [b, r] = await Promise.all([
			s.api.GET('/api/v1/role-bindings'),
			s.api.GET('/api/v1/roles')
		]);
		if (!b.data) {
			error = apiError(b.error);
			return;
		}
		bindings = b.data.items;
		roles = r.data?.items.map((x) => x.role) ?? [];
	}

	$effect(() => {
		getSession()
			.then((s) => {
				session = s;
				return load(s);
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	function scopeOf(f: typeof form): string {
		return f.kind === 'platform' ? 'platform' : `${f.kind}:${f.key}`;
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		formError = undefined;
		const { error: err } = await session.api.POST('/api/v1/role-bindings', {
			body: { claim: form.claim, value: form.value, role: form.role, scope: scopeOf(form) }
		});
		if (err) {
			formError = apiError(err);
			return;
		}
		form = { ...form, value: '', key: '' };
		await load(session);
	}

	async function remove(b: Schemas['RoleBinding']) {
		if (!session || !confirm(`Remove ${b.role} for ${b.claim}=${b.value} at ${b.scope}?`)) return;
		const { error: err } = await session.api.DELETE('/api/v1/role-bindings/{id}', {
			params: { path: { id: b.id } }
		});
		if (err) {
			error = apiError(err);
			return;
		}
		await load(session);
	}
</script>

<svelte:head><title>Access · Ballet</title></svelte:head>

<h1>Access</h1>
<p class="muted">
	Role bindings grant a role at a scope to everyone whose token claim matches the value.
</p>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !bindings}
	<p class="muted">Loading…</p>
{:else}
	<div class="table-wrap">
		<table>
			<thead>
				<tr><th>Claim</th><th>Value</th><th>Role</th><th>Scope</th><th></th></tr>
			</thead>
			<tbody>
				{#each bindings as b (b.id)}
					<tr>
						<td class="mono">{b.claim}</td>
						<td class="mono">{b.value}</td>
						<td>{b.role}</td>
						<td class="mono">{b.scope}</td>
						<td>
							{#if b.bootstrap}
								<span class="badge" title="Configured in rbac.bootstrap_platform_admins">bootstrap</span>
							{:else}
								<button
									onclick={() => remove(b)}
									aria-label={`Remove ${b.role} binding for ${b.value}`}>Remove</button
								>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<h2>Grant access</h2>
	<form class="form card" onsubmit={create} aria-label="Grant access">
		<label>Claim <input bind:value={form.claim} required /></label>
		<label>Value <input bind:value={form.value} required placeholder="acme-devs" /></label>
		<label
			>Role
			<select bind:value={form.role}>
				{#each roles as r (r)}<option value={r}>{r}</option>{/each}
			</select>
		</label>
		<label
			>Scope
			<select bind:value={form.kind}>
				<option value="platform">platform</option>
				<option value="customer">customer</option>
				<option value="project">project</option>
			</select>
		</label>
		{#if form.kind !== 'platform'}
			<label
				>{form.kind === 'customer' ? 'Customer key' : 'Project key'}
				<input bind:value={form.key} required /></label
			>
		{/if}
		<button class="primary" type="submit">Grant</button>
		{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	</form>
{/if}
