import { expect, test } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

test('admins set a project budget and see a used-up daily budget', async ({ page }) => {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role: 'customer-admin', scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web', description: '', version: 1 }
		],
		budgets: {
			'project:WEB': { ticket_tokens: 0, daily_tokens: 0, used_today: 120000, version: 0 }
		}
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/projects/WEB/settings');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const budget = page.getByRole('region', { name: 'Budget' });
	await expect(budget.getByRole('status')).toContainText('120,000');
	await expect(budget.getByRole('status')).toContainText('no daily limit');
	await budget.getByRole('spinbutton', { name: 'Per ticket' }).fill('50000');
	await budget.getByRole('spinbutton', { name: 'Per day' }).fill('100000');
	await budget.getByRole('button', { name: 'Save budget' }).click();
	await expect(budget.getByText('Saved.')).toBeVisible();
	await expect(budget.getByRole('status')).toContainText('the daily budget is used up');
	expect(core.budgets['project:WEB']).toMatchObject({
		ticket_tokens: 50000,
		daily_tokens: 100000,
		version: 1
	});
});

test('viewers see the budget read-only', async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page, {
		me: [{ role: 'viewer', scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web', description: '', version: 1 }]
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/customers/acme');
	await page.getByRole('button', { name: 'Sign in' }).click();
	const budget = page.getByRole('region', { name: 'Budget' });
	await expect(budget.getByRole('spinbutton', { name: 'Per day' })).toBeDisabled();
	await expect(budget.getByRole('button', { name: 'Save budget' })).toHaveCount(0);
});
