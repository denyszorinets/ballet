import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeItem } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

function ticket(n: number, state: string, title: string): FakeItem {
	return {
		id: `t${n}`,
		key: `WEB-${n}`,
		project: 'WEB',
		kind: 'ticket',
		title,
		description: '',
		state,
		type: 'feature',
		acceptance_criteria: [],
		policy: { review_mode: 'agent', merge_mode: 'auto' },
		version: 1
	};
}

async function open(page: Page, role: string, path: string) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		items: [ticket(1, 'ready', 'Schema'), ticket(2, 'ready', 'API'), ticket(3, 'done', 'Setup')],
		deps: [{ id: 'd1', from: 'WEB-1', to: 'WEB-2', type: 'blocks' }]
	});
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('the board groups tickets by state and marks blocked ones', async ({ page }) => {
	await open(page, 'engineer', '/projects/WEB');

	const ready = page.getByRole('listitem', { name: 'Ready' });
	await expect(ready.getByRole('link')).toHaveCount(2);
	await expect(ready.getByRole('link', { name: /API/ })).toContainText('Blocked');
	await expect(ready.getByRole('link', { name: /Schema/ })).not.toContainText('Blocked');
	await expect(page.getByRole('listitem', { name: 'Done' }).getByRole('link')).toHaveCount(1);

	await page.getByLabel('New ticket').fill('Payments');
	await page.getByRole('button', { name: 'Add' }).click();
	await expect(
		page.getByRole('listitem', { name: 'Backlog' }).getByRole('link', { name: /Payments/ })
	).toBeVisible();
});

test('the item page edits, transitions and manages dependencies', async ({ page }) => {
	await open(page, 'engineer', '/items/WEB-2');

	await expect(page.getByRole('heading', { name: 'API' })).toBeVisible();
	await expect(page.getByRole('listitem').filter({ hasText: 'Blocked by' })).toContainText('WEB-1');

	await page.getByRole('button', { name: 'Edit' }).click();
	await page.getByLabel('Acceptance criteria (one per line)').fill('Returns 200\n\nDocumented');
	await page.getByLabel('Review').selectOption('agent+human');
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Documented')).toBeVisible();
	await expect(page.getByText('Review agent+human')).toBeVisible();

	await page.getByRole('button', { name: 'Move to Paused' }).click();
	await expect(page.getByTestId('state')).toHaveText('Paused');

	await page.getByRole('button', { name: 'Remove dependency on WEB-1' }).click();
	await expect(page.getByText('No dependencies.')).toBeVisible();
	await page.getByLabel('Item key').fill('WEB-3');
	await page.getByLabel('Relation').selectOption('relates');
	await page.getByRole('button', { name: 'Add dependency' }).click();
	await expect(page.getByRole('listitem').filter({ hasText: 'Relates to' })).toContainText('WEB-3');

	await page.getByLabel('Item key').fill('WEB-99');
	await page.getByRole('button', { name: 'Add dependency' }).click();
	await expect(page.getByRole('alert')).toContainText('WEB-99 does not exist');
});

test('viewers get a read-only board and item page', async ({ page }) => {
	await open(page, 'viewer', '/projects/WEB');

	await expect(page.getByRole('listitem', { name: 'Ready' }).getByRole('link')).toHaveCount(2);
	await expect(page.getByLabel('New ticket')).toHaveCount(0);
	await page.getByRole('link', { name: /API/ }).click();
	await expect(page.getByRole('heading', { name: 'API' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: /Move to/ })).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'Add dependency' })).toHaveCount(0);
});

test('the board fits a phone screen without page-level horizontal scrolling', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await open(page, 'engineer', '/projects/WEB');
	await expect(page.getByRole('listitem', { name: 'Ready' })).toBeVisible();

	const overflow = await page.evaluate(
		() => document.documentElement.scrollWidth - window.innerWidth
	);
	expect(overflow).toBeLessThanOrEqual(0);
});
