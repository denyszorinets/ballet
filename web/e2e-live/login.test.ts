import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { expect, test, type Page } from '@playwright/test';

const repo = path.resolve(import.meta.dirname, '../..');

/** Creates an organization through Core as alice, so the test needs no seeded data. */
async function createOrganization(key: string, name: string) {
	const token = execFileSync(path.join(repo, 'scripts/dev-token.sh'), ['alice']).toString().trim();
	const res = await fetch('http://localhost:8080/api/v1/organizations', {
		method: 'POST',
		headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
		body: JSON.stringify({ key, name })
	});
	if (!res.ok) throw new Error(`create organization: ${res.status} ${await res.text()}`);
}

async function signIn(page: Page, user: string) {
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByLabel(/username/i).fill(user);
	await page
		.getByLabel(/password/i)
		.first()
		.fill(user);
	await page.getByRole('button', { name: /sign in/i }).click();
}

test('alice signs in through Keycloak, sees organizations and the live connection', async ({
	page
}) => {
	const errors: string[] = [];
	page.on(
		'console',
		(m) => m.type() === 'error' && errors.push(`${m.text()} @ ${m.location().url}`)
	);
	page.on('response', (r) => r.status() >= 400 && errors.push(`${r.status()} ${r.url()}`));

	const name = `Login Test ${Date.now().toString(36)}`;
	await createOrganization(`login-${Date.now().toString(36)}`, name);
	await signIn(page, 'alice');

	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();
	await expect(page.getByTestId('user-name')).toHaveText('Alice Admin');
	await expect(page.getByText(name)).toBeVisible();
	await expect(page.getByRole('status')).toHaveText('Live');
	await page.screenshot({ path: 'test-results/live-alice-light.png', fullPage: true });

	await page.getByRole('button', { name: 'Switch to dark theme' }).click();
	await page.screenshot({ path: 'test-results/live-alice-dark.png', fullPage: true });

	await page.reload();
	await expect(page.getByTestId('user-name')).toHaveText('Alice Admin', { timeout: 10_000 });

	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
	expect(errors).toEqual([]);
});

test('carol without role bindings sees no organizations', async ({ page }) => {
	await signIn(page, 'carol');

	await expect(page.getByText('There are no organizations you can see yet.')).toBeVisible();
});
