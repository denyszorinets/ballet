import { expect, test } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeRealtime } from './fixtures/realtime';

test('without an identity provider the app needs no sign-in', async ({ page }) => {
	await page.route('**/config.json', (r) =>
		r.fulfill({ json: { oidc: { issuer: '', client_id: 'ballet-web' } } })
	);
	let auth = '';
	await fakeCore(page, {
		me: [{ role: 'platform-admin', scope: 'platform' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }]
	});
	page.on('request', (r) => {
		if (r.url().includes('/api/v1/customers')) auth = r.headers()['authorization'] ?? '';
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/');

	await expect(page.getByRole('heading', { name: 'Customers' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Sign in' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Sign out' })).toHaveCount(0);
	await expect(page.getByTestId('user-name')).toHaveText('Local user');
	await expect(page.getByRole('link', { name: 'acme' })).toBeVisible();
	expect(auth).toBe('Bearer local');
});
