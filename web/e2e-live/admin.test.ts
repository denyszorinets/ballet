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
	await expect(page.getByRole('heading', { name: 'Customers' })).toBeVisible();
}

test('alice administers a customer; bob gets exactly the granted access', async ({ browser }) => {
	const suffix = Date.now().toString(36);
	const customer = `live-${suffix}`;
	const project = `L${suffix
		.toUpperCase()
		.replace(/[^A-Z0-9]/g, '')
		.slice(0, 8)}`;
	const errors: string[] = [];

	const alice = await browser.newPage();
	alice.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
	await signIn(alice, 'alice');

	const newCustomer = alice.getByRole('form', { name: 'New customer' });
	await newCustomer.getByLabel('Key').fill(customer);
	await newCustomer.getByLabel('Name').fill('Live Test Inc');
	await newCustomer.getByRole('button', { name: 'Create customer' }).click();
	await alice.getByRole('link', { name: new RegExp(customer) }).click();

	await expect(alice.getByRole('heading', { name: 'Live Test Inc' })).toBeVisible();
	await alice.getByRole('button', { name: 'Rename' }).click();
	await alice
		.getByRole('form', { name: 'Rename customer' })
		.getByLabel('Name')
		.fill('Live Test Corp');
	await alice.getByRole('button', { name: 'Save' }).click();
	await expect(alice.getByRole('heading', { name: 'Live Test Corp' })).toBeVisible();

	const newProject = alice.getByRole('form', { name: 'New project' });
	await newProject.getByLabel('Key').fill(project);
	await newProject.getByLabel('Name').fill('Live project');
	await newProject.getByRole('button', { name: 'Create project' }).click();
	await expect(alice.getByRole('cell', { name: project })).toBeVisible();
	await alice.screenshot({ path: 'test-results/live-customer.png', fullPage: true });

	await alice.getByRole('link', { name: 'Access' }).click();
	const grant = alice.getByRole('form', { name: 'Grant access' });
	await grant.getByLabel('Value').fill('acme-devs');
	await grant.getByLabel('Role').selectOption('engineer');
	await grant.getByLabel('Scope').selectOption('customer');
	await grant.getByLabel('Customer key').fill(customer);
	await grant.getByRole('button', { name: 'Grant' }).click();
	await expect(alice.getByRole('cell', { name: `customer:${customer}` })).toBeVisible();
	await alice.screenshot({ path: 'test-results/live-access.png', fullPage: true });

	const bob = await (await browser.newContext()).newPage();
	await signIn(bob, 'bob');
	await expect(bob.getByRole('link', { name: new RegExp(customer) })).toBeVisible();
	await expect(bob.getByRole('form', { name: 'New customer' })).toHaveCount(0);
	await expect(bob.getByRole('link', { name: 'Access' })).toHaveCount(0);
	await bob.getByRole('link', { name: new RegExp(customer) }).click();
	await expect(bob.getByRole('cell', { name: project })).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Rename' })).toHaveCount(0);
	await expect(bob.getByRole('form', { name: 'New project' })).toHaveCount(0);

	expect(errors).toEqual([]);
});
