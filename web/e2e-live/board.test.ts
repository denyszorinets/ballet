import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { expect, test, type Page } from '@playwright/test';

const repo = path.resolve(import.meta.dirname, '../..');

function token(user: string): string {
	return execFileSync(path.join(repo, 'scripts/dev-token.sh'), [user]).toString().trim();
}

async function api(method: string, p: string, body?: unknown) {
	const res = await fetch(`http://localhost:8080/api/v1${p}`, {
		method,
		headers: { Authorization: `Bearer ${token('alice')}`, 'Content-Type': 'application/json' },
		body: body ? JSON.stringify(body) : undefined
	});
	if (!res.ok) throw new Error(`${method} ${p}: ${res.status} ${await res.text()}`);
	return res.status === 204 ? undefined : res.json();
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
	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();
}

test('board and item pages update live from other clients', async ({ page }) => {
	const suffix = Date.now()
		.toString(36)
		.toUpperCase()
		.replace(/[^A-Z0-9]/g, '');
	const project = `B${suffix.slice(-8)}`;
	await api('POST', '/organizations', { key: `board-${suffix.toLowerCase()}`, name: 'Board test' });
	await api('POST', `/organizations/board-${suffix.toLowerCase()}/projects`, {
		key: project,
		name: 'Board project'
	});
	const errors: string[] = [];
	page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));

	await signIn(page, 'alice');
	await page.goto(`/projects/${project}`);
	await expect(page.getByRole('heading', { name: 'Board project' })).toBeVisible();

	// Create two tickets through the UI.
	for (const title of ['Schema', 'API']) {
		await page.getByLabel('New ticket').fill(title);
		await page.getByRole('button', { name: 'Add' }).click();
		await expect(page.getByRole('link', { name: new RegExp(title) })).toBeVisible();
	}
	const backlog = page.getByRole('listitem', { name: 'Backlog' });
	await expect(backlog.getByRole('link')).toHaveCount(2);

	// Another client: both ready, API blocked by Schema.
	await api('POST', `/items/${project}-1/transition`, { state: 'ready', version: 1 });
	await api('POST', `/items/${project}-2/transition`, { state: 'ready', version: 1 });
	await api('POST', `/items/${project}-2/dependencies`, {
		type: 'blocked_by',
		item: `${project}-1`
	});

	const ready = page.getByRole('listitem', { name: 'Ready' });
	await expect(ready.getByRole('link')).toHaveCount(2);
	await expect(ready.getByRole('link', { name: /API/ }).getByText('Blocked')).toBeVisible();
	await page.screenshot({ path: 'test-results/live-board.png', fullPage: true });

	// Schema done elsewhere → API unblocked live.
	await api('POST', `/items/${project}-1/transition`, { state: 'done', version: 2 });
	await expect(
		page.getByRole('listitem', { name: 'Done' }).getByRole('link', { name: /Schema/ })
	).toBeVisible();
	await expect(ready.getByText('Blocked')).toHaveCount(0);

	// Item page: edit, see dependency, transition, live update from elsewhere.
	await page.getByRole('link', { name: /API/ }).click();
	await expect(page.getByRole('heading', { name: 'API' })).toBeVisible();
	await expect(page.getByRole('listitem').filter({ hasText: 'Blocked by' })).toContainText(
		`${project}-1`
	);
	await page.getByRole('button', { name: 'Edit' }).click();
	await page.getByLabel('Acceptance criteria (one per line)').fill('Returns 200\nDocumented');
	await page.getByLabel('Merge').selectOption('manual');
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Documented')).toBeVisible();
	await expect(page.getByText('Merge manual')).toBeVisible();
	await page.getByRole('button', { name: 'Move to Paused' }).click();
	await expect(page.getByTestId('state')).toHaveText('Paused');

	const item = (await api('GET', `/items/${project}-2`)) as { version: number };
	await api('PATCH', `/items/${project}-2`, { version: item.version, title: 'API v2' });
	await expect(page.getByRole('heading', { name: 'API v2' })).toBeVisible();
	await page.screenshot({ path: 'test-results/live-item.png', fullPage: true });

	expect(errors).toEqual([]);
});
