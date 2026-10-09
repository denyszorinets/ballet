import { expect, test } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

test('a platform admin creates and renames an organization and adds a project', async ({
	page
}) => {
	await fakeOIDC(page);
	await fakeCore(page, { me: [{ role: 'platform-admin', scope: 'platform' }] });
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const form = page.getByRole('form', { name: 'New organization' });
	await form.getByLabel('Key').fill('acme');
	await form.getByLabel('Name').fill('Acme');
	await form.getByRole('button', { name: 'Create organization' }).click();
	await page.getByRole('link', { name: /acme/ }).click();

	await expect(page.getByRole('heading', { name: 'Acme' })).toBeVisible();
	await page.getByRole('button', { name: 'Rename' }).click();
	await page
		.getByRole('form', { name: 'Rename organization' })
		.getByLabel('Name')
		.fill('Acme Corp');
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(page.getByRole('heading', { name: 'Acme Corp' })).toBeVisible();

	const project = page.getByRole('form', { name: 'New project' });
	await project.getByLabel('Key').fill('WEB');
	await project.getByLabel('Name').fill('Web shop');
	await project.getByRole('button', { name: 'Create project' }).click();
	await expect(page.getByRole('cell', { name: 'WEB', exact: true })).toBeVisible();
});

test('API errors are shown on the form', async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page, {
		me: [{ role: 'platform-admin', scope: 'platform' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }]
	});
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const form = page.getByRole('form', { name: 'New organization' });
	await form.getByLabel('Key').fill('acme');
	await form.getByLabel('Name').fill('Again');
	await form.getByRole('button', { name: 'Create organization' }).click();

	await expect(form.getByRole('alert')).toContainText('already exists');
});

test('access page lists, grants and removes role bindings', async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page, {
		me: [{ role: 'platform-admin', scope: 'platform' }],
		bindings: [
			{
				id: 'boot',
				claim: 'groups',
				value: 'ballet-admins',
				role: 'platform-admin',
				scope: 'platform',
				bootstrap: true
			}
		]
	});
	page.on('dialog', (d) => d.accept());
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await page.getByRole('link', { name: 'Access' }).click();
	await expect(page.getByText('bootstrap')).toBeVisible();
	const grant = page.getByRole('form', { name: 'Grant access' });
	await grant.getByLabel('Value').fill('acme-devs');
	await grant.getByLabel('Role').selectOption('viewer');
	await grant.getByLabel('Scope').selectOption('project');
	await grant.getByLabel('Project key').fill('WEB');
	await grant.getByRole('button', { name: 'Grant' }).click();
	await expect(page.getByRole('cell', { name: 'project:WEB' })).toBeVisible();

	await page.getByRole('button', { name: 'Remove viewer binding for acme-devs' }).click();
	await expect(page.getByRole('cell', { name: 'project:WEB' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: /Remove platform-admin/ })).toHaveCount(0);
});

test('viewers see no administration controls', async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page, {
		me: [{ role: 'viewer', scope: 'organization:acme' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }]
	});
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page.getByRole('link', { name: /acme/ })).toBeVisible();
	await expect(page.getByRole('form', { name: 'New organization' })).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Access' })).toHaveCount(0);
	await page.getByRole('link', { name: /acme/ }).click();
	await expect(page.getByRole('heading', { name: 'Acme' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Rename' })).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'New project' })).toHaveCount(0);
});
