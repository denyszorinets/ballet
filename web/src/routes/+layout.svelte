<script lang="ts">
	import '../app.css';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { getSession, type Session } from '$lib/session';
	import type { Status } from '$lib/realtime/client';
	import { currentTheme, setTheme, storedTheme, type Theme } from '$lib/theme';

	let { children } = $props();

	let session = $state<Session>();
	let loadError = $state<string>();
	let status = $state<Status>('idle');
	let theme = $state<Theme>('light');
	/** Open questions waiting for a human. */
	let inbox = $state(0);

	const onCallback = $derived(page.url.pathname === '/auth/callback');
	const authenticated = $derived(session?.auth.authenticated ?? false);

	const statusLabel: Record<Status, string> = {
		idle: 'Offline',
		connecting: 'Connecting',
		open: 'Live',
		reconnecting: 'Reconnecting',
		unauthenticated: 'Signed out',
		closed: 'Offline'
	};

	$effect(() => {
		const stored = storedTheme();
		if (stored) document.documentElement.dataset.theme = stored;
		theme = currentTheme();
	});

	$effect(() => {
		getSession()
			.then((s) => (session = s))
			.catch((e: unknown) => (loadError = e instanceof Error ? e.message : String(e)));
	});

	$effect(() => {
		if (session && authenticated && !session.permissions.loaded) {
			session.permissions.load(session.api).catch(() => {});
		}
	});

	$effect(() => {
		if (!session || !authenticated) return;
		session.realtime.connect();
		return session.realtime.onStatus((s) => (status = s));
	});

	$effect(() => {
		if (!session || !authenticated) return;
		const s = session;
		void page.url.pathname; // recount on navigation
		s.api
			.GET('/api/v1/inbox')
			.then(({ data }) => {
				if (data) inbox = data.items.filter((q) => q.route !== 'planner').length;
			})
			.catch(() => {});
	});

	function toggleTheme() {
		theme = theme === 'dark' ? 'light' : 'dark';
		setTheme(theme);
	}
</script>

{#if loadError}
	<main class="center">
		<div class="card">
			<h1>Ballet is unavailable</h1>
			<p class="error">{loadError}</p>
		</div>
	</main>
{:else if !session}
	<main class="center"><p class="muted">Loading…</p></main>
{:else if onCallback}
	{@render children()}
{:else if !authenticated}
	<main class="center">
		<div class="card signin">
			<h1>Ballet</h1>
			<p class="muted">Orchestrated AI software development.</p>
			<button
				class="primary"
				onclick={() => session?.auth.login(page.url.pathname + page.url.search)}
			>
				Sign in
			</button>
		</div>
	</main>
{:else}
	<header>
		<a class="brand" href={resolve('/')}>Ballet</a>
		<nav aria-label="Main">
			<a
				href={resolve('/')}
				aria-current={page.url.pathname === '/' || page.url.pathname.startsWith('/customers')
					? 'page'
					: undefined}>Customers</a
			>
			<a
				href={resolve('/inbox')}
				aria-current={page.url.pathname.startsWith('/inbox') ? 'page' : undefined}
				>Inbox{#if inbox > 0}<span class="count" aria-label="{inbox} open">{inbox}</span>{/if}</a
			>
			<a
				href={resolve('/skills')}
				aria-current={page.url.pathname.startsWith('/skills') ? 'page' : undefined}>Skills</a
			>
			{#if session.permissions.managesAccess}
				<a
					href={resolve('/access')}
					aria-current={page.url.pathname === '/access' ? 'page' : undefined}>Access</a
				>
			{/if}
		</nav>
		<div class="spacer"></div>
		<span class="status" data-status={status} title="Realtime connection" role="status">
			<span class="dot" aria-hidden="true"></span>{statusLabel[status]}
		</span>
		<button
			class="icon"
			onclick={toggleTheme}
			aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
		>
			<svg
				viewBox="0 0 24 24"
				width="16"
				height="16"
				aria-hidden="true"
				fill="none"
				stroke="currentColor"
				stroke-width="2"
				stroke-linecap="round"
			>
				{#if theme === 'dark'}
					<circle cx="12" cy="12" r="4" />
					<path
						d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"
					/>
				{:else}
					<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
				{/if}
			</svg>
		</button>
		<span class="user" data-testid="user-name">{session.auth.displayName}</span>
		<button onclick={() => session?.auth.logout()}>Sign out</button>
	</header>
	<main class="content">
		{@render children()}
	</main>
{/if}

<style>
	header {
		display: flex;
		align-items: center;
		gap: 1rem;
		padding: 0.6rem 1.25rem;
		background: var(--surface);
		border-bottom: 1px solid var(--border);
		flex-wrap: wrap;
	}
	.brand {
		font-weight: 700;
		font-size: 1.1rem;
		color: var(--text);
		text-decoration: none;
	}
	nav {
		display: flex;
		gap: 1rem;
	}
	nav a {
		color: var(--muted);
		text-decoration: none;
	}
	.count {
		margin-left: 0.3rem;
		padding: 0 0.4rem;
		border-radius: 999px;
		background: var(--accent);
		color: var(--bg);
		font-size: 0.75rem;
		font-weight: 600;
	}
	nav a[aria-current='page'] {
		color: var(--text);
		font-weight: 600;
	}
	.spacer {
		flex: 1;
	}
	.status {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
		font-size: 0.85rem;
		color: var(--muted);
	}
	.dot {
		width: 0.55rem;
		height: 0.55rem;
		border-radius: 50%;
		background: var(--muted);
	}
	.status[data-status='open'] .dot {
		background: var(--ok);
	}
	.status[data-status='connecting'] .dot,
	.status[data-status='reconnecting'] .dot {
		background: var(--warn);
	}
	.icon {
		display: inline-flex;
		padding: 0.45rem;
	}
	.user {
		font-size: 0.9rem;
	}
	.content {
		max-width: 72rem;
		margin: 0 auto;
		padding: 1.5rem 1rem;
	}
	.center {
		min-height: 100vh;
		display: grid;
		place-items: center;
		padding: 1rem;
	}
	.signin {
		text-align: center;
		min-width: min(22rem, 100%);
	}
</style>
