import { expect, test, type Page } from '@playwright/test';

async function signIn(page: Page, user: string) {
	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByLabel(/username/i).fill(user);
	await page
		.getByLabel(/password/i)
		.first()
		.fill(user);
	await page.getByRole('button', { name: /sign in/i }).click();
}

test('alice signs in through Keycloak, sees customers and the live connection', async ({
	page
}) => {
	const errors: string[] = [];
	page.on(
		'console',
		(m) => m.type() === 'error' && errors.push(`${m.text()} @ ${m.location().url}`)
	);
	page.on('response', (r) => r.status() >= 400 && errors.push(`${r.status()} ${r.url()}`));

	await signIn(page, 'alice');

	await expect(page.getByRole('heading', { name: 'Customers' })).toBeVisible();
	await expect(page.getByTestId('user-name')).toHaveText('Alice Admin');
	await expect(page.getByText('Acme Corporation')).toBeVisible();
	await expect(page.getByRole('status')).toHaveText('Live');
	await page.screenshot({ path: 'test-results/live-alice-light.png', fullPage: true });

	await page.getByRole('button', { name: 'Switch to dark theme' }).click();
	await page.screenshot({ path: 'test-results/live-alice-dark.png', fullPage: true });

	await page.reload();
	await expect(page.getByTestId('user-name')).toHaveText('Alice Admin', { timeout: 10_000 });

	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
	expect(errors).toEqual([]);
});

test('carol without role bindings sees no customers', async ({ page }) => {
	await signIn(page, 'carol');

	await expect(page.getByText('There are no customers you can see yet.')).toBeVisible();
});
