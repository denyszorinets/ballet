import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// SPA: every route falls back to index.html; Core serves the bundle.
			adapter: adapter({ fallback: 'index.html' })
		})
	],
	server: {
		// Forward API traffic to a locally running Core (go run ./core/cmd/core).
		proxy: {
			'/api': 'http://127.0.0.1:8080',
			'/healthz': 'http://127.0.0.1:8080',
			'/rpc': { target: 'ws://127.0.0.1:8080', ws: true }
		}
	},
	test: {
		expect: { requireAssertions: true },
		projects: [
			{
				extends: './vite.config.ts',
				server: {
					// Forward API traffic to a locally running Core (go run ./core/cmd/core).
					proxy: {
						'/api': 'http://127.0.0.1:8080',
						'/healthz': 'http://127.0.0.1:8080',
						'/rpc': { target: 'ws://127.0.0.1:8080', ws: true }
					}
				},
				test: {
					name: 'server',
					environment: 'node',
					include: ['src/**/*.{test,spec}.{js,ts}'],
					exclude: ['src/**/*.svelte.{test,spec}.{js,ts}']
				}
			}
		]
	}
});
