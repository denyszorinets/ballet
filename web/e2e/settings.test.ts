import { expect, test, type Page } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

async function open(page: Page, role: string) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		]
	});
	await page.goto('/projects/WEB');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByRole('main').getByRole('link', { name: 'Settings' }).click();
	return core;
}

test('admins configure execution and the git token', async ({ page }) => {
	const core = await open(page, 'customer-admin');
	const form = page.getByRole('form', { name: 'Execution settings' });
	await form.getByLabel('Repository URL').fill('https://github.com/acme/web.git');
	await form.getByLabel('Default branch').fill('main');
	await form.getByLabel('Image').fill('golang:1.27');
	await form.getByLabel('Setup commands (one per line)').fill('make deps\n\ngo mod download');
	await form.getByLabel('Environment (KEY=value per line)').fill('CI=1\nGOFLAGS=-mod=mod');
	await form.getByLabel('Answer window (minutes)').fill('30');
	await form.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Saved.')).toBeVisible();
	expect(core.execution.WEB).toMatchObject({
		repo_url: 'https://github.com/acme/web.git',
		setup: ['make deps', 'go mod download'],
		env: { CI: '1', GOFLAGS: '-mod=mod' },
		answer_window_minutes: 30,
		version: 1
	});

	await form.getByLabel('Environment (KEY=value per line)').fill('oops');
	await form.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByRole('alert')).toContainText('is not KEY=value');

	const token = page.getByRole('form', { name: 'Git token' });
	await token.getByLabel('Token').fill('ghp_secret1234');
	await token.getByRole('button', { name: 'Save token' }).click();
	await expect(page.getByText('Git token saved (abcd…1234).')).toBeVisible();
	await expect(token.getByLabel('Token')).toHaveValue('');
	expect(core.gitTokens.WEB).toBe('ghp_secret1234');
});

test('engineers see the settings read-only and no token form', async ({ page }) => {
	await open(page, 'engineer');
	const form = page.getByRole('form', { name: 'Execution settings' });
	await expect(form.getByLabel('Repository URL')).toBeDisabled();
	await expect(form.getByRole('button', { name: 'Save' })).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'Git token' })).toHaveCount(0);
});
