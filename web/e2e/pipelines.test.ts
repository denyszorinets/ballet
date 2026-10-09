import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

async function open(page: Page, role: string): Promise<FakeCore> {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'organization:acme' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', organization: 'acme', name: 'Web', description: '', version: 1 }
		]
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/projects/WEB/pipelines');
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('an admin edits the default pipeline and publishes a new version', async ({ page }) => {
	const core = await open(page, 'organization-admin');
	const stages = page.getByRole('list', { name: 'Stages' });
	await expect(stages.getByRole('listitem')).toHaveCount(3);
	await expect(page.getByText('not published yet')).toBeVisible();
	await expect(page.getByRole('status').filter({ hasText: 'Valid.' })).toBeVisible();

	// Add a human approval before the merge.
	await page.getByRole('button', { name: 'Add stage' }).click();
	const added = stages.getByRole('listitem').nth(3);
	await added.getByRole('textbox', { name: 'ID' }).fill('review');
	await expect(page.getByRole('alert', { name: 'Problems' })).toContainText('duplicate id review');
	await expect(page.getByRole('button', { name: 'Publish default' })).toBeDisabled();
	await added.getByRole('textbox', { name: 'ID' }).fill('approve');
	await added.getByRole('combobox', { name: 'Kind' }).selectOption('human');
	await added.getByRole('combobox', { name: 'When failed' }).selectOption('implement');
	await page.getByRole('button', { name: 'Move approve up' }).click();
	await expect(stages.getByRole('listitem').nth(2)).toContainText('3');
	await expect(page.getByRole('status').filter({ hasText: 'Valid.' })).toBeVisible();

	// The YAML view shows the same pipeline.
	await page.getByRole('tab', { name: 'YAML' }).click();
	await expect(page.getByRole('textbox', { name: 'Pipeline YAML' })).toHaveValue(/"approve"/);
	await page.getByRole('tab', { name: 'Stages' }).click();

	await page.getByRole('button', { name: 'Publish default' }).click();
	await expect(page.getByText('Published default version 1.')).toBeVisible();
	const published = core.pipelines.default[0].definition as {
		stages: { id: string; kind: string }[];
	};
	expect(published.stages.map((s) => s.id)).toEqual([
		'implement',
		'review',
		'approve',
		'integrate'
	]);
	expect(published.stages[2].kind).toBe('human');
	await expect(page.getByRole('table')).toContainText('user-alice');
	await expect(page.getByText('version 1', { exact: true })).toBeVisible();
});

test('invalid YAML stays in the YAML view with its problems', async ({ page }) => {
	await open(page, 'organization-admin');
	await page.getByRole('tab', { name: 'YAML' }).click();
	await page
		.getByRole('textbox', { name: 'Pipeline YAML' })
		.fill('{"stages": [], "max_iterations": 99}');
	await page.getByRole('tab', { name: 'Stages' }).click();
	await expect(page.getByRole('alert', { name: 'Problems' })).toContainText(
		'max_iterations must be 1-20'
	);
});

test('engineers read pipelines without editing', async ({ page }) => {
	await open(page, 'engineer');
	await expect(page.getByRole('list', { name: 'Stages' }).getByRole('listitem')).toHaveCount(3);
	await expect(page.getByRole('button', { name: /Publish/ })).toHaveCount(0);
	await expect(page.getByRole('textbox', { name: 'ID' }).first()).toBeDisabled();
});
