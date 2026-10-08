<script lang="ts">
	import BudgetEditor from '$lib/components/BudgetEditor.svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError, type Schemas } from '$lib/api/client';
	import { getSession, type Session } from '$lib/session';

	const project = $derived(page.params.project ?? '');

	let session = $state<Session>();
	let customer = $state<string>();
	let settings = $state<Schemas['ExecutionSettings']>();
	let error = $state<string>();
	let saveError = $state<string>();
	let saved = $state(false);

	let form = $state({
		repo_url: '',
		default_branch: '',
		image: '',
		setup: '',
		env: '',
		branch_template: '',
		git_name: '',
		git_email: '',
		forge: '' as '' | 'github' | 'git',
		forge_api_url: '',
		link_template: '',
		answer_window_minutes: 0
	});
	let token = $state('');
	let tokenMessage = $state<string>();

	const canEdit = $derived(!!session?.permissions.can('project.update', { customer, project }));
	const canManageToken = $derived(
		!!session?.permissions.can('credential.manage', { customer, project })
	);

	function fill(x: Schemas['ExecutionSettings']) {
		settings = x;
		form = {
			repo_url: x.repo_url,
			default_branch: x.default_branch,
			image: x.image,
			setup: x.setup.join('\n'),
			env: Object.entries(x.env)
				.map(([k, v]) => `${k}=${v}`)
				.join('\n'),
			branch_template: x.branch_template,
			git_name: x.git_name,
			git_email: x.git_email,
			forge: x.forge,
			forge_api_url: x.forge_api_url,
			link_template: x.link_template,
			answer_window_minutes: x.answer_window_minutes
		};
	}

	async function load(s: Session, key: string) {
		const path = { params: { path: { project: key } } };
		const [p, x] = await Promise.all([
			s.api.GET('/api/v1/projects/{project}', path),
			s.api.GET('/api/v1/projects/{project}/execution', path)
		]);
		if (!x.data) {
			error = apiError(x.error);
			return;
		}
		customer = p.data?.customer;
		fill(x.data);
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

	const lines = (s: string) =>
		s
			.split('\n')
			.map((l) => l.trim())
			.filter(Boolean);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!session || !settings) return;
		saveError = undefined;
		saved = false;
		const env: Record<string, string> = {};
		for (const l of lines(form.env)) {
			const i = l.indexOf('=');
			if (i <= 0) {
				saveError = `Environment line "${l}" is not KEY=value.`;
				return;
			}
			env[l.slice(0, i)] = l.slice(i + 1);
		}
		const { data, error: err } = await session.api.PUT('/api/v1/projects/{project}/execution', {
			params: { path: { project } },
			body: {
				...form,
				setup: lines(form.setup),
				env,
				version: settings.version
			}
		});
		if (!data) {
			saveError = apiError(err);
			return;
		}
		fill(data);
		saved = true;
	}

	async function setToken(e: SubmitEvent) {
		e.preventDefault();
		if (!session) return;
		const { data, error: err } = await session.api.PUT(
			'/api/v1/projects/{project}/credentials/{provider}',
			{ params: { path: { project, provider: 'git' } }, body: { api_key: token } }
		);
		token = '';
		tokenMessage = data ? `Git token saved (${data.fingerprint}).` : apiError(err);
	}
</script>

<svelte:head><title>Settings · {project} · Ballet</title></svelte:head>

<p><a href={resolve('/projects/[project]', { project })}>← {project}</a></p>
<h1>Execution settings</h1>
<p class="muted">
	How agent runs of this project execute: the repository they work on, the devcontainer image, and
	commands that prepare the workspace.
</p>

{#if error}
	<p class="error" role="alert">{error}</p>
{:else if !settings}
	<p class="muted">Loading…</p>
{:else}
	<form class="settings card" onsubmit={save} aria-label="Execution settings">
		<fieldset disabled={!canEdit}>
			<label
				>Repository URL <input
					bind:value={form.repo_url}
					placeholder="https://github.com/acme/web.git"
				/></label
			>
			<label>Default branch <input bind:value={form.default_branch} placeholder="main" /></label>
			<label
				>Branch template <input
					bind:value={form.branch_template}
					placeholder="ballet/{'{ticket}'}-{'{slug}'}"
				/></label
			>
			<label>Image <input bind:value={form.image} placeholder="golang:1.27" /></label>
			<label
				>Setup commands (one per line) <textarea bind:value={form.setup} rows="3"></textarea></label
			>
			<label
				>Environment (KEY=value per line) <textarea bind:value={form.env} rows="3"
				></textarea></label
			>
			<label>Commit name <input bind:value={form.git_name} placeholder="Ballet Agent" /></label>
			<label
				>Commit email <input
					bind:value={form.git_email}
					placeholder="agent@ballet.invalid"
				/></label
			>
			<label
				>Forge
				<select bind:value={form.forge}>
					<option value="">Automatic (GitHub for github.com, else plain git)</option>
					<option value="github">GitHub</option>
					<option value="git">Plain git (no pull requests)</option>
				</select>
			</label>
			<label
				>GitHub API URL (Enterprise) <input
					bind:value={form.forge_api_url}
					placeholder="https://api.github.com"
				/></label
			>
			<label
				>Branch link template (plain git) <input
					bind:value={form.link_template}
					placeholder="https://git.example.com/web/compare/{'{base}'}...{'{branch}'}"
				/></label
			>
			<label
				>Answer window (minutes) <input
					type="number"
					min="0"
					max="1440"
					bind:value={form.answer_window_minutes}
					aria-describedby="answer-window-help"
				/></label
			>
			<p class="muted small" id="answer-window-help">
				How long a session waits for answers to its blocking questions before it parks and continues
				later. 0: Ballet's default (15 minutes).
			</p>
			{#if canEdit}<button class="primary" type="submit">Save</button>{/if}
		</fieldset>
		{#if saveError}<p class="error" role="alert">{saveError}</p>{/if}
		{#if saved}<p class="ok">Saved.</p>{/if}
	</form>

	{#if canManageToken}
		<h2>Git token</h2>
		<p class="muted">
			Used to clone and push. It is stored encrypted, sent to runs only when they start, and never
			shown again.
		</p>
		<form class="form card" onsubmit={setToken} aria-label="Git token">
			<label class="grow">Token <input type="password" bind:value={token} required /></label>
			<button type="submit">Save token</button>
		</form>
		{#if tokenMessage}<p role="status">{tokenMessage}</p>{/if}
	{/if}

	{#if customer}<BudgetEditor {customer} {project} />{/if}
{/if}

<style>
	.settings fieldset {
		border: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.settings label {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.settings textarea {
		font-family: ui-monospace, monospace;
	}
	.grow {
		flex: 1;
		min-width: 12rem;
	}
	.ok {
		color: var(--ok);
	}
</style>
