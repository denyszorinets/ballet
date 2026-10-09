import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { expect, test } from '@playwright/test';

const repo = path.resolve(import.meta.dirname, '../..');

async function api(method: string, p: string, body?: unknown) {
	const token = execFileSync(path.join(repo, 'scripts/dev-token.sh'), ['alice']).toString().trim();
	const res = await fetch(`http://localhost:8080/api/v1${p}`, {
		method,
		headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
		body: body ? JSON.stringify(body) : undefined
	});
	if (!res.ok) throw new Error(`${method} ${p}: ${res.status} ${await res.text()}`);
	return res.json();
}

// Needs the Knowledge service on :8081 behind Core (Core [knowledge] url).
test('alice writes, edits, finds and links knowledge through Core', async ({ page }) => {
	const suffix = Date.now().toString(36);
	const organization = `kn-${suffix}`;
	const project = `K${suffix
		.toUpperCase()
		.replace(/[^A-Z0-9]/g, '')
		.slice(-8)}`;
	await api('POST', '/organizations', { key: organization, name: 'Knowledge test' });
	await api('POST', `/organizations/${organization}/projects`, {
		key: project,
		name: 'Knowledge project'
	});
	const ticket = await api('POST', `/projects/${project}/items`, {
		kind: 'ticket',
		title: 'Cache'
	});
	const errors: string[] = [];
	page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));

	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByLabel(/username/i).fill('alice');
	await page
		.getByLabel(/password/i)
		.first()
		.fill('alice');
	await page.getByRole('button', { name: /sign in/i }).click();
	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();

	await page.goto(`/items/${ticket.key}`);
	await expect(page.getByText('No linked knowledge.')).toBeVisible();
	await page.getByRole('link', { name: 'Add knowledge' }).click();
	await page.getByLabel('Kind').selectOption('decision');
	await page.getByLabel('Title').fill('Cache invalidation strategy');
	await page.getByLabel('Body (Markdown)').fill('## Decision\n\nUse **write-through** caching.');
	await page.getByRole('button', { name: 'Create entry' }).click();
	await expect(page.getByRole('heading', { name: 'Cache invalidation strategy' })).toBeVisible();
	await expect(page.getByTestId('body').locator('strong')).toHaveText('write-through');

	await page.getByRole('button', { name: 'Edit' }).click();
	await page.getByLabel('Body (Markdown)').fill('## Decision\n\nUse **write-behind** caching.');
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Version 2')).toBeVisible();
	await page.getByRole('list', { name: 'Versions' }).getByRole('button', { name: 'v1' }).click();
	await expect(page.getByTestId('body').locator('strong')).toHaveText('write-through');
	await page.screenshot({ path: 'test-results/live-knowledge-entry.png', fullPage: true });

	await page.getByRole('link', { name: '← Knowledge' }).click();
	await page.getByRole('searchbox', { name: 'Search' }).fill('write-behind');
	await page.getByRole('button', { name: 'Search' }).click();
	await expect(
		page
			.getByRole('list', { name: 'Search results' })
			.getByRole('link', { name: 'Cache invalidation strategy' })
	).toBeVisible();
	await page.screenshot({ path: 'test-results/live-knowledge-search.png', fullPage: true });

	await page.goto(`/items/${ticket.key}`);
	await expect(
		page
			.getByRole('list', { name: 'Linked knowledge' })
			.getByRole('link', { name: 'Cache invalidation strategy' })
	).toBeVisible();

	expect(errors).toEqual([]);
});
