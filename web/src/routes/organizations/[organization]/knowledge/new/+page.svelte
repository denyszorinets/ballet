<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { apiError } from '$lib/api/client';
	import KnowledgeEditor, { type Draft } from '$lib/components/KnowledgeEditor.svelte';
	import { getSession } from '$lib/session';

	const organization = $derived(page.params.organization ?? '');
	let error = $state<string>();

	// A link from a tracker item pre-fills the item and its project.
	const item = page.url.searchParams.get('item') ?? '';
	const initial: Draft = {
		kind: 'document',
		title: '',
		body: '',
		projects: item ? [item.replace(/-\d+$/, '')] : [],
		items: item ? [item] : []
	};

	async function create(d: Draft) {
		error = undefined;
		const s = await getSession();
		const { data, error: err } = await s.api.POST(
			'/api/v1/organizations/{organization}/knowledge/entries',
			{
				params: { path: { organization } },
				body: d
			}
		);
		if (!data) {
			error = apiError(err);
			return;
		}
		await goto(
			resolve('/organizations/[organization]/knowledge/[entry]', { organization, entry: data.id })
		);
	}
</script>

<svelte:head><title>New entry · Knowledge · Ballet</title></svelte:head>

<h1>New knowledge entry</h1>
<KnowledgeEditor
	{initial}
	submitLabel="Create entry"
	{error}
	onsubmit={create}
	oncancel={() => goto(resolve('/organizations/[organization]/knowledge', { organization }))}
/>
