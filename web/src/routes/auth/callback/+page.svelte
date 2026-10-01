<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getSession } from '$lib/session';

	let error = $state<string>();

	$effect(() => {
		getSession()
			.then(async (s) => {
				const returnTo = await s.auth.completeLogin(window.location.href);
				await s.permissions.load(s.api).catch(() => {});
				return returnTo;
			})
			// returnTo is a same-origin path validated by Auth.completeLogin.
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			.then((returnTo) => goto(returnTo, { replaceState: true }))
			.catch((e: unknown) => (error = e instanceof Error ? e.message : String(e)));
	});
</script>

<main class="center">
	{#if error}
		<div class="card">
			<h1>Sign-in failed</h1>
			<p class="error">{error}</p>
			<a href={resolve('/')}>Back to Ballet</a>
		</div>
	{:else}
		<p class="muted">Signing in…</p>
	{/if}
</main>

<style>
	.center {
		min-height: 100vh;
		display: grid;
		place-items: center;
	}
</style>
