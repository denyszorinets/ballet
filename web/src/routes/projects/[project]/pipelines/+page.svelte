<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	type Definition = Schemas['PipelineDefinition'];
	type Stage = Schemas['PipelineStage'];
	type Outcome = 'done' | 'failed' | 'blocked';

	const project = $derived(page.params.project ?? '');
	const outcomes: Outcome[] = ['done', 'failed', 'blocked'];
	const ticketTypes = ['feature', 'bug', 'tech_debt', 'docs', 'spike'];
	const specialTargets = [
		{ value: '$done', label: 'finish ($done)' },
		{ value: '$failed', label: 'fail ($failed)' },
		{ value: '$question', label: 'ask ($question)' }
	];

	let session = $state<Session>();
	let customer = $state<string>();
	let pipelines = $state<Schemas['Pipeline'][]>([]);
	let name = $state('default');
	/** The version the editor started from (0: Ballet's template). */
	let baseVersion = $state(0);
	let definition = $state<Definition>({ stages: [], max_iterations: 3 });
	let versions = $state<Schemas['Pipeline'][]>([]);
	let tab = $state<'stages' | 'yaml'>('stages');
	let yaml = $state('');
	let errors = $state<string[]>([]);
	let error = $state<string>();
	let notice = $state<string>();
	let loaded = $state(false);
	let publishing = $state(false);

	const canEdit = $derived(!!session?.permissions.can('project.update', { customer, project }));
	const stageIDs = $derived(definition.stages.map((s) => s.id));

	async function loadList(s: Session) {
		const [p, list] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', { params: { path: { project } } }),
			s.api.GET('/api/v1/projects/{project}/pipelines', { params: { path: { project } } })
		]);
		if (!list.data) {
			error = apiError(list.error);
			return;
		}
		customer = p.data?.customer;
		pipelines = list.data.items;
	}

	async function open(s: Session, n: string, version?: number) {
		error = notice = undefined;
		const path = { project, name: n };
		const [p, vs] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}/pipelines/{name}', {
				params: { path, query: version ? { version } : {} }
			}),
			s.api.GET('/api/v1/projects/{project}/pipelines/{name}/versions', { params: { path } })
		]);
		versions = vs.data?.items ?? [];
		if (!p.data) {
			// A new pipeline for a ticket type starts from the default one.
			const def = pipelines.find((x) => x.name === 'default');
			if (!def) {
				error = apiError(p.error);
				return;
			}
			definition = structuredClone($state.snapshot(def.definition));
			baseVersion = 0;
		} else {
			definition = structuredClone(p.data.definition);
			baseVersion = versions[0]?.version ?? p.data.version;
		}
		name = n;
		tab = 'stages';
		loaded = true;
		await validate();
	}

	$effect(() => {
		const key = project;
		void key;
		getSession()
			.then(async (s) => {
				session = s;
				await loadList(s);
				await open(s, 'default');
			})
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});

	/** Validates the definition on the server (render, then parse). */
	async function validate(): Promise<boolean> {
		if (!session) return false;
		const r = await session.api.POST('/api/v1/pipelines/render', {
			body: { definition: $state.snapshot(definition) }
		});
		if (!r.data) {
			errors = [apiError(r.error)];
			return false;
		}
		yaml = r.data.yaml;
		const p = await session.api.POST('/api/v1/pipelines/parse', { body: { yaml } });
		errors = p.data?.errors ?? [apiError(p.error)];
		return errors.length === 0;
	}

	let timer: ReturnType<typeof setTimeout> | undefined;
	function changed() {
		notice = undefined;
		clearTimeout(timer);
		timer = setTimeout(() => void validate(), 400);
	}

	/** Takes the YAML text into the editor. */
	async function fromYAML(): Promise<boolean> {
		if (!session) return false;
		const p = await session.api.POST('/api/v1/pipelines/parse', { body: { yaml } });
		if (!p.data) {
			errors = [apiError(p.error)];
			return false;
		}
		errors = p.data.errors;
		if (p.data.definition) definition = p.data.definition;
		return errors.length === 0 || !!p.data.definition;
	}

	async function switchTab(t: 'stages' | 'yaml') {
		if (t === tab) return;
		if (t === 'yaml') await validate();
		else if (!(await fromYAML())) return; // stay on YAML until it parses
		tab = t;
	}

	function addStage() {
		let n = definition.stages.length + 1;
		while (stageIDs.includes(`stage${n}`)) n++;
		definition.stages.push({ id: `stage${n}`, kind: 'agent', instructions: '' });
		changed();
	}

	function move(i: number, by: number) {
		const j = i + by;
		if (j < 0 || j >= definition.stages.length) return;
		const s = definition.stages;
		[s[i], s[j]] = [s[j], s[i]];
		changed();
	}

	function remove(i: number) {
		definition.stages.splice(i, 1);
		changed();
	}

	function setKind(st: Stage, kind: Stage['kind']) {
		st.kind = kind;
		if (kind === 'platform') st.action = 'merge';
		else delete st.action;
		if (kind !== 'agent') {
			delete st.instructions;
			delete st.adapter;
			delete st.model;
			delete st.skills;
		}
		changed();
	}

	function setNext(st: Stage, o: Outcome, target: string) {
		const next = { ...(st.next ?? {}) };
		if (target) next[o] = target;
		else delete next[o];
		st.next = Object.keys(next).length ? next : undefined;
		changed();
	}

	function setSkills(st: Stage, text: string) {
		const skills = text
			.split(',')
			.map((x) => x.trim())
			.filter(Boolean);
		st.skills = skills.length ? skills : undefined;
		changed();
	}

	async function publish() {
		if (!session) return;
		if (tab === 'yaml' && !(await fromYAML())) return;
		if (!(await validate())) return;
		publishing = true;
		error = notice = undefined;
		const { data, error: err } = await session.api.PUT(
			'/api/v1/projects/{project}/pipelines/{name}',
			{
				params: { path: { project, name } },
				body: { definition: $state.snapshot(definition), version: baseVersion }
			}
		);
		publishing = false;
		if (!data) {
			error = apiError(err);
			return;
		}
		await loadList(session);
		await open(session, name);
		notice = `Published ${name} version ${data.version}. New tickets run on it; running ones keep their version.`;
	}

	function exportYAML() {
		const url = URL.createObjectURL(new Blob([yaml], { type: 'application/yaml' }));
		const a = document.createElement('a');
		a.href = url;
		a.download = `${project}-${name}.yaml`;
		a.click();
		URL.revokeObjectURL(url);
	}

	async function importYAML(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0];
		if (!file) return;
		yaml = await file.text();
		tab = 'yaml';
		await fromYAML();
	}

	const label = (st: Stage) => st.name || st.id;
	function defaultTarget(i: number, o: Outcome): string {
		if (o !== 'done') return 'ask ($question)';
		const next = definition.stages[i + 1];
		return next ? `next: ${label(next)}` : 'finish ($done)';
	}
</script>

<svelte:head><title>Pipelines · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Pipelines</h1>
<p class="muted">
	The stages a ticket runs through. A ticket uses the pipeline named after its type, else
	<code>default</code>. Publishing creates a new version; running tickets keep theirs.
</p>

{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if loaded}
	<div class="bar">
		<label
			>Pipeline
			<select
				value={name}
				onchange={(e) => session && open(session, (e.target as HTMLSelectElement).value)}
			>
				<option value="default">default</option>
				{#each ticketTypes as t (t)}
					<option value={t}
						>{t}{pipelines.some((p) => p.name === t) ? '' : ' (uses default)'}</option
					>
				{/each}
			</select>
		</label>
		<span class="muted small">
			{baseVersion === 0
				? 'not published yet (Ballet template / default)'
				: `version ${baseVersion}`}
		</span>
		<div class="spacer"></div>
		<button onclick={exportYAML} disabled={!yaml}>Export YAML</button>
		{#if canEdit}
			<label class="file"
				>Import YAML<input type="file" accept=".yaml,.yml" onchange={importYAML} /></label
			>
		{/if}
	</div>

	<div class="tabs" role="tablist" aria-label="Editor">
		<button role="tab" aria-selected={tab === 'stages'} onclick={() => switchTab('stages')}
			>Stages</button
		>
		<button role="tab" aria-selected={tab === 'yaml'} onclick={() => switchTab('yaml')}>YAML</button
		>
	</div>

	{#if tab === 'stages'}
		<fieldset disabled={!canEdit} class="editor">
			<label class="inline"
				>Loop limit
				<input
					type="number"
					min="1"
					max="20"
					bind:value={definition.max_iterations}
					oninput={changed}
				/></label
			>
			<ol class="stages" aria-label="Stages">
				{#each definition.stages as st, i (i)}
					<li class="card stage">
						<div class="row">
							<span class="num muted">{i + 1}</span>
							<label>ID <input bind:value={st.id} oninput={changed} class="mono" size="12" /></label
							>
							<label>Name <input bind:value={st.name} oninput={changed} size="16" /></label>
							<label
								>Kind
								<select
									value={st.kind}
									onchange={(e) =>
										setKind(st, (e.target as HTMLSelectElement).value as Stage['kind'])}
								>
									<option value="agent">agent session</option>
									<option value="human">human approval</option>
									<option value="platform">platform (merge)</option>
								</select>
							</label>
							<div class="spacer"></div>
							<button
								aria-label="Move {label(st)} up"
								onclick={() => move(i, -1)}
								disabled={i === 0}>↑</button
							>
							<button
								aria-label="Move {label(st)} down"
								onclick={() => move(i, 1)}
								disabled={i === definition.stages.length - 1}>↓</button
							>
							<button aria-label="Remove {label(st)}" onclick={() => remove(i)}>✕</button>
						</div>
						{#if st.kind === 'agent'}
							<label
								>Instructions
								<textarea bind:value={st.instructions} oninput={changed} rows="3"></textarea>
							</label>
							<div class="row">
								<label
									>Model <input
										bind:value={st.model}
										oninput={changed}
										placeholder="default"
									/></label
								>
								<label
									>Skills <input
										value={(st.skills ?? []).join(', ')}
										oninput={(e) => setSkills(st, (e.target as HTMLInputElement).value)}
										placeholder="all project skills"
									/></label
								>
								<label
									>Timeout (min) <input
										type="number"
										min="0"
										bind:value={st.timeout_minutes}
										oninput={changed}
										placeholder="120"
									/></label
								>
							</div>
						{/if}
						<div class="row next" aria-label="Transitions of {label(st)}">
							{#each outcomes as o (o)}
								<label
									>When {o}
									<select
										value={st.next?.[o] ?? ''}
										onchange={(e) => setNext(st, o, (e.target as HTMLSelectElement).value)}
									>
										<option value="">{defaultTarget(i, o)}</option>
										{#each definition.stages as other (other.id)}
											{#if other.id !== st.id}<option value={other.id}>go to {label(other)}</option
												>{/if}
										{/each}
										{#each specialTargets as t (t.value)}
											<option value={t.value}>{t.label}</option>
										{/each}
									</select>
								</label>
							{/each}
						</div>
					</li>
				{/each}
			</ol>
			<button onclick={addStage}>Add stage</button>
		</fieldset>
	{:else}
		<label class="yaml"
			><span class="visually-hidden">Pipeline YAML</span>
			<textarea
				bind:value={yaml}
				rows="24"
				class="mono"
				readonly={!canEdit}
				oninput={() => {
					notice = undefined;
				}}></textarea>
		</label>
		<button onclick={fromYAML}>Check</button>
	{/if}

	{#if errors.length}
		<ul class="errors" role="alert" aria-label="Problems">
			{#each errors as e (e)}<li>{e}</li>{/each}
		</ul>
	{:else}
		<p class="ok small" role="status">Valid.</p>
	{/if}

	{#if canEdit}
		<button class="primary" onclick={publish} disabled={publishing || errors.length > 0}
			>Publish {name}</button
		>
	{/if}
	{#if notice}<p class="ok" role="status">{notice}</p>{/if}

	<h2>History</h2>
	{#if versions.length === 0}
		<p class="muted">
			Never published: tickets run on Ballet's template{name === 'default' ? '' : ' or default'}.
		</p>
	{:else}
		<table>
			<thead><tr><th>Version</th><th>Published by</th><th>At</th><th></th></tr></thead>
			<tbody>
				{#each versions as v (v.version)}
					<tr>
						<td>{v.version}</td>
						<td>{v.created_by}</td>
						<td>{v.created_at ? new Date(v.created_at).toLocaleString() : ''}</td>
						<td
							><button
								onclick={async () => {
									if (!session) return;
									const base = versions[0]?.version ?? 0;
									await open(session, name, v.version);
									baseVersion = base;
									notice = `Loaded version ${v.version}; publish to make it current again.`;
								}}>Load</button
							></td
						>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
{/if}

<style>
	.bar,
	.row {
		display: flex;
		gap: 0.75rem;
		align-items: flex-end;
		flex-wrap: wrap;
	}
	.bar {
		margin: 1rem 0;
	}
	.spacer {
		flex: 1;
	}
	.small {
		font-size: 0.85rem;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 0.2rem;
	}
	label.inline {
		flex-direction: row;
		align-items: center;
	}
	.tabs {
		display: flex;
		gap: 0.25rem;
		border-bottom: 1px solid var(--border);
		margin-bottom: 0.75rem;
	}
	.tabs button[aria-selected='true'] {
		border-color: var(--accent);
		font-weight: 600;
	}
	.editor {
		border: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		align-items: flex-start;
	}
	.stages {
		list-style: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
		width: 100%;
	}
	.stage {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.num {
		font-weight: 600;
		align-self: center;
	}
	textarea {
		width: 100%;
		box-sizing: border-box;
		font: inherit;
	}
	.yaml textarea {
		font-family: ui-monospace, monospace;
	}
	.errors {
		color: var(--danger);
	}
	.ok {
		color: var(--ok);
	}
	.file input {
		font-size: 0.85rem;
	}
	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}
</style>
