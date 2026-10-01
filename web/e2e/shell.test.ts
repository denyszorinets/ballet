import { expect, test } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

test.beforeEach(async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page);
	await page.route('**/api/v1/customers', (r) =>
		r.fulfill({
			json: {
				items: [
					{
						id: '1',
						key: 'acme',
						name: 'Acme Corporation',
						created_at: '2026-10-01T00:00:00Z',
						updated_at: '2026-10-01T00:00:00Z',
						version: 1
					}
				]
			}
		})
	);
});

test('signs in, shows the shell and customers, and signs out', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();

	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page.getByRole('heading', { name: 'Customers' })).toBeVisible();
	await expect(page.getByTestId('user-name')).toHaveText('Alice Admin');
	await expect(page.getByText('Acme Corporation')).toBeVisible();
	await expect(page).toHaveURL('/');

	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
});

test('sends the access token to the API', async ({ page }) => {
	let authorization: string | null = null;
	await page.route('**/api/v1/customers', (r) => {
		authorization = r.request().headers()['authorization'] ?? null;
		return r.fulfill({ json: { items: [] } });
	});

	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page.getByText('There are no customers you can see yet.')).toBeVisible();
	expect(authorization).toMatch(/^Bearer ey/);
});

test('returns to the page the user started on', async ({ page }) => {
	await page.goto('/?view=compact');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page).toHaveURL('/?view=compact');
});

test('shows API errors', async ({ page }) => {
	await page.route('**/api/v1/customers', (r) =>
		r.fulfill({ status: 403, json: { error: 'forbidden', message: 'forbidden: customer.read' } })
	);

	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page.getByRole('alert')).toHaveText('forbidden: customer.read');
});

test('toggles and remembers the theme', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	const html = page.locator('html');

	await page.getByRole('button', { name: 'Switch to dark theme' }).click();
	await expect(html).toHaveAttribute('data-theme', 'dark');

	await page.reload();
	await expect(html).toHaveAttribute('data-theme', 'dark');
	await expect(page.getByRole('button', { name: 'Switch to light theme' })).toBeVisible();
});

test('reports when Core is unreachable', async ({ page }) => {
	await page.unroute('**/config.json');
	await page.route('**/config.json', (r) => r.fulfill({ status: 502, body: 'bad gateway' }));

	await page.goto('/');

	await expect(page.getByRole('heading', { name: 'Ballet is unavailable' })).toBeVisible();
});
