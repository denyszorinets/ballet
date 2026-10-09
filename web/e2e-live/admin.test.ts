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
	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();
}

test('alice administers an organization; bob gets exactly the granted access', async ({
	browser
}) => {
	const suffix = Date.now().toString(36);
	const organization = `live-${suffix}`;
	const project = `L${suffix
		.toUpperCase()
		.replace(/[^A-Z0-9]/g, '')
		.slice(0, 8)}`;
	const errors: string[] = [];

	const alice = await browser.newPage();
	alice.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
	await signIn(alice, 'alice');

	const newOrganization = alice.getByRole('form', { name: 'New organization' });
	await newOrganization.getByLabel('Key').fill(organization);
	await newOrganization.getByLabel('Name').fill('Live Test Inc');
	await newOrganization.getByRole('button', { name: 'Create organization' }).click();
	await alice.getByRole('link', { name: new RegExp(organization) }).click();

	await expect(alice.getByRole('heading', { name: 'Live Test Inc' })).toBeVisible();
	await alice.getByRole('button', { name: 'Rename' }).click();
	await alice
		.getByRole('form', { name: 'Rename organization' })
		.getByLabel('Name')
		.fill('Live Test Corp');
	await alice.getByRole('button', { name: 'Save' }).click();
	await expect(alice.getByRole('heading', { name: 'Live Test Corp' })).toBeVisible();

	const newProject = alice.getByRole('form', { name: 'New project' });
	await newProject.getByLabel('Key').fill(project);
	await newProject.getByLabel('Name').fill('Live project');
	await newProject.getByRole('button', { name: 'Create project' }).click();
	await expect(alice.getByRole('cell', { name: project })).toBeVisible();
	await alice.screenshot({ path: 'test-results/live-organization.png', fullPage: true });

	await alice.getByRole('link', { name: 'Access' }).click();
	const grant = alice.getByRole('form', { name: 'Grant access' });
	await grant.getByLabel('Value').fill('acme-devs');
	await grant.getByLabel('Role').selectOption('engineer');
	await grant.getByLabel('Scope').selectOption('organization');
	await grant.getByLabel('Organization key').fill(organization);
	await grant.getByRole('button', { name: 'Grant' }).click();
	await expect(alice.getByRole('cell', { name: `organization:${organization}` })).toBeVisible();
	await alice.screenshot({ path: 'test-results/live-access.png', fullPage: true });

	const bob = await (await browser.newContext()).newPage();
	await signIn(bob, 'bob');
	await expect(bob.getByRole('link', { name: new RegExp(organization) })).toBeVisible();
	await expect(bob.getByRole('form', { name: 'New organization' })).toHaveCount(0);
	await expect(bob.getByRole('link', { name: 'Access' })).toHaveCount(0);
	await bob.getByRole('link', { name: new RegExp(organization) }).click();
	await expect(bob.getByRole('cell', { name: project })).toBeVisible();
	await expect(bob.getByRole('button', { name: 'Rename' })).toHaveCount(0);
	await expect(bob.getByRole('form', { name: 'New project' })).toHaveCount(0);

	expect(errors).toEqual([]);
});
