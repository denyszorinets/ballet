<script lang="ts">
	import { renderMarkdown } from '$lib/markdown';
	import { KINDS, kindLabel, parseKeys, type KnowledgeKind } from '$lib/knowledge';

	export interface Draft {
		kind: KnowledgeKind;
		title: string;
		body: string;
		projects: string[];
		items: string[];
	}

	let {
		initial,
		submitLabel,
		error,
		onsubmit,
		oncancel
	}: {
		initial: Draft;
		submitLabel: string;
		error?: string;
		onsubmit: (d: Draft) => void;
		oncancel?: () => void;
	} = $props();

	// The form edits a copy; the initial value is only read once.
	// svelte-ignore state_referenced_locally
	let kind = $state(initial.kind);
	// svelte-ignore state_referenced_locally
	let title = $state(initial.title);
	// svelte-ignore state_referenced_locally
	let body = $state(initial.body);
	// svelte-ignore state_referenced_locally
	let projects = $state(initial.projects.join(', '));
	// svelte-ignore state_referenced_locally
	let items = $state(initial.items.join(', '));
	let preview = $state(false);

	function submit(e: SubmitEvent) {
		e.preventDefault();
		onsubmit({ kind, title, body, projects: parseKeys(projects), items: parseKeys(items) });
	}
</script>

<form class="editor" onsubmit={submit} aria-label="Knowledge entry">
	<div class="form">
		<label
			>Kind
			<select bind:value={kind}>
				{#each KINDS as k (k)}<option value={k}>{kindLabel[k]}</option>{/each}
			</select>
		</label>
		<label class="grow">Title <input bind:value={title} required /></label>
	</div>

	<div class="tabs" role="tablist" aria-label="Body">
		<button
			type="button"
			role="tab"
			aria-selected={!preview}
			class:active={!preview}
			onclick={() => (preview = false)}>Write</button
		>
		<button
			type="button"
			role="tab"
			aria-selected={preview}
			class:active={preview}
			onclick={() => (preview = true)}>Preview</button
		>
	</div>
	{#if preview}
		<div class="markdown card" data-testid="preview">
			<!-- eslint-disable-next-line svelte/no-at-html-tags -- sanitized by renderMarkdown -->
			{@html renderMarkdown(body)}
		</div>
	{:else}
		<label class="body"
			><span class="visually-hidden">Body (Markdown)</span>
			<textarea bind:value={body} rows="16" placeholder="Markdown"></textarea>
		</label>
	{/if}

	<div class="form">
		<label class="grow">Projects <input bind:value={projects} placeholder="WEB, API" /> </label>
		<label class="grow"
			>Linked items <input bind:value={items} placeholder="WEB-12, WEB-14" />
		</label>
	</div>

	<div class="actions">
		<button class="primary" type="submit">{submitLabel}</button>
		{#if oncancel}<button type="button" onclick={oncancel}>Cancel</button>{/if}
	</div>
	{#if error}<p class="error" role="alert">{error}</p>{/if}
</form>

<style>
	.editor {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.grow {
		flex: 1;
		min-width: 12rem;
	}
	.tabs {
		display: flex;
		gap: 0.25rem;
	}
	.tabs .active {
		font-weight: 600;
		border-color: currentColor;
	}
	.body textarea {
		width: 100%;
		box-sizing: border-box;
		font-family: ui-monospace, monospace;
	}
	.actions {
		display: flex;
		gap: 0.5rem;
	}
	.visually-hidden {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}
</style>
