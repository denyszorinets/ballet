<script lang="ts">
	import { fetchHealth, type Health } from '$lib/api/health';

	let health = $state<Health | undefined>();

	$effect(() => {
		fetchHealth().then((h) => (health = h));
	});
</script>

<main>
	<h1>Ballet</h1>
	<p data-testid="core-status">
		Core: {health ? health.status : 'checking…'}
	</p>
</main>
