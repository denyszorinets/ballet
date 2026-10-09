import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeEntry } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

const adr: FakeEntry = {
	id: 'k1',
	organization: 'acme',
	kind: 'decision',
	title: 'Use SQLite',
	body: '# Context\n\nWe want **simple** storage.',
	projects: ['WEB'],
	items: ['WEB-1'],
	version: 1
};
const note: FakeEntry = {
	id: 'k2',
	organization: 'acme',
	kind: 'note',
	title: 'Deploy checklist',
	body: 'Run migrations first.',
	projects: [],
	items: [],
	version: 1
};

async function open(page: Page, role: string, path: string) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'organization:acme' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', organization: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		items: [
			{
				id: 't1',
				key: 'WEB-1',
				project: 'WEB',
				kind: 'ticket',
				title: 'Storage',
				description: '',
				state: 'ready',
				type: 'feature',
				acceptance_criteria: [],
				policy: { review_mode: 'agent', merge_mode: 'auto' },
				version: 1
			}
		],
		knowledge: [structuredClone(adr), structuredClone(note)]
	});
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('the knowledge page lists, filters and searches entries', async ({ page }) => {
	await open(page, 'viewer', '/organizations/acme/knowledge');

	const entries = page.getByRole('list', { name: 'Entries' });
	await expect(entries.getByRole('listitem')).toHaveCount(2);
	await expect(page.getByRole('link', { name: 'New entry' })).toHaveCount(0);

	await page.getByLabel('Kind').selectOption('decision');
	await page.getByRole('button', { name: 'Search' }).click();
	await expect(entries.getByRole('listitem')).toHaveCount(1);
	await expect(entries).toContainText('Use SQLite');

	await page.getByLabel('Kind').selectOption('');
	await page.getByRole('searchbox', { name: 'Search' }).fill('migrations');
	await page.getByRole('button', { name: 'Search' }).click();
	const results = page.getByRole('list', { name: 'Search results' });
	await expect(results.getByRole('listitem')).toHaveCount(1);
	await expect(results).toContainText('Deploy checklist');

	await page.getByRole('searchbox', { name: 'Search' }).fill('kubernetes');
	await page.getByRole('button', { name: 'Search' }).click();
	await expect(page.getByText('Nothing matches “kubernetes”.')).toBeVisible();
});

test('an entry renders sanitized Markdown, links and versions', async ({ page }) => {
	const core = await open(page, 'engineer', '/organizations/acme/knowledge/k1');

	const body = page.getByTestId('body');
	await expect(body.getByRole('heading', { name: 'Context' })).toBeVisible();
	await expect(body.locator('strong')).toHaveText('simple');
	await expect(page.getByRole('link', { name: 'WEB-1' })).toHaveAttribute('href', '/items/WEB-1');

	await page.getByRole('button', { name: 'Edit' }).click();
	await page
		.getByLabel('Body (Markdown)')
		.fill('# Context\n\nPlus <img src=x onerror="window.pwned=1">');
	await page.getByRole('tab', { name: 'Preview' }).click();
	await expect(page.getByTestId('preview').getByRole('heading', { name: 'Context' })).toBeVisible();
	await page.getByRole('button', { name: 'Save' }).click();

	await expect(page.getByText('Version 2')).toBeVisible();
	await expect(body).toContainText('Plus');
	expect(await page.evaluate(() => 'pwned' in window)).toBe(false);
	expect(core.knowledge[0].version).toBe(2);

	const versions = page.getByRole('list', { name: 'Versions' });
	await expect(versions.getByRole('listitem')).toHaveCount(2);
	await versions.getByRole('button', { name: 'v1' }).click();
	await expect(page.getByText('Showing version 1 by user-alice.')).toBeVisible();
	await expect(body.locator('strong')).toHaveText('simple');
	await page.getByRole('button', { name: 'Show current' }).click();
	await expect(body).toContainText('Plus');
});

test('a viewer cannot edit an entry', async ({ page }) => {
	await open(page, 'viewer', '/organizations/acme/knowledge/k1');
	await expect(page.getByRole('heading', { name: 'Use SQLite' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
});

test('knowledge is created from a tracker item and linked back', async ({ page }) => {
	const core = await open(page, 'engineer', '/items/WEB-1');

	const linked = page.getByRole('list', { name: 'Linked knowledge' });
	await expect(linked.getByRole('link', { name: 'Use SQLite' })).toBeVisible();

	await page.getByRole('link', { name: 'Add knowledge' }).click();
	await expect(page.getByLabel('Linked items')).toHaveValue('WEB-1');
	await expect(page.getByLabel('Projects')).toHaveValue('WEB');
	await page.getByLabel('Kind').selectOption('debt');
	await page.getByLabel('Title').fill('Missing retries');
	await page.getByLabel('Body (Markdown)').fill('Storage calls do not retry.');
	await page.getByRole('button', { name: 'Create entry' }).click();

	await expect(page.getByRole('heading', { name: 'Missing retries' })).toBeVisible();
	expect(core.knowledge.at(-1)).toMatchObject({
		kind: 'debt',
		items: ['WEB-1'],
		projects: ['WEB']
	});

	await page.getByRole('link', { name: 'WEB-1' }).click();
	await expect(linked.getByRole('link')).toHaveCount(2);
});
