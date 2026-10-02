<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import type { Schemas } from '$lib/api/client';
	import PlannerChat from '$lib/components/PlannerChat.svelte';

	const id = $derived(page.params.session ?? '');
	let info = $state<Schemas['PlannerTranscript']>();
</script>

<svelte:head><title>{info?.title ?? 'Planner'} · Ballet</title></svelte:head>

{#if info}
	<p>
		<a href={resolve('/projects/[project]/planner', { project: info.project })}
			>← Planner · {info.project}</a
		>
	</p>
	<h1>{info.title}</h1>
{/if}
{#key id}
	<PlannerChat {id} onload={(i) => (info = i)} />
{/key}
