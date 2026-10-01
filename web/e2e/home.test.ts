import { expect, test } from '@playwright/test';

test('home page shows Core as healthy when Core responds', async ({ page }) => {
	await page.route('**/healthz', (route) =>
		route.fulfill({ json: { service: 'core', status: 'ok' } })
	);

	await page.goto('/');

	await expect(page.getByRole('heading', { name: 'Ballet' })).toBeVisible();
	await expect(page.getByTestId('core-status')).toHaveText('Core: ok');
});

test('home page shows Core as unreachable when Core is down', async ({ page }) => {
	await page.route('**/healthz', (route) => route.fulfill({ status: 502 }));

	await page.goto('/');

	await expect(page.getByTestId('core-status')).toHaveText('Core: unreachable');
});
