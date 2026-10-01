import { defineConfig, devices } from '@playwright/test';

// Live end-to-end tests against a running development stack: Keycloak on
// :8180, Core on :8080 and the Vite dev server on :5173 (see the
// frontend-debugging skill). Not run in CI.
const runningAsRoot = process.getuid?.() === 0;

export default defineConfig({
	testDir: 'e2e-live',
	reporter: 'list',
	use: {
		baseURL: process.env.BALLET_URL ?? 'http://localhost:5173',
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure'
	},
	projects: [
		{
			name: 'chrome',
			use: {
				...devices['Desktop Chrome'],
				channel: 'chrome',
				launchOptions: { args: runningAsRoot ? ['--no-sandbox'] : [] }
			}
		}
	]
});
