import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { expect, test } from '@playwright/test';

const repo = path.resolve(import.meta.dirname, '../..');

async function api(method: string, p: string, body?: unknown) {
	const token = execFileSync(path.join(repo, 'scripts/dev-token.sh'), ['alice']).toString().trim();
	const res = await fetch(`http://localhost:8080/api/v1${p}`, {
		method,
		headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
		body: body ? JSON.stringify(body) : undefined
	});
	if (!res.ok) throw new Error(`${method} ${p}: ${res.status} ${await res.text()}`);
	return res.json();
}

test('alice writes, publishes, compares and pins a skill', async ({ page }) => {
	const suffix = Date.now().toString(36);
	const organization = `sk-${suffix}`;
	const project = `S${suffix
		.toUpperCase()
		.replace(/[^A-Z0-9]/g, '')
		.slice(-8)}`;
	await api('POST', '/organizations', { key: organization, name: 'Skill test' });
	await api('POST', `/organizations/${organization}/projects`, {
		key: project,
		name: 'Skill project'
	});
	const errors: string[] = [];
	page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));

	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByLabel(/username/i).fill('alice');
	await page
		.getByLabel(/password/i)
		.first()
		.fill('alice');
	await page.getByRole('button', { name: /sign in/i }).click();
	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();

	await page.goto(`/skills?scope=organization:${organization}`);
	const form = page.getByRole('form', { name: 'New skill' });
	await form.getByLabel('Name').fill('release-notes');
	await form.getByLabel('Description').fill('How to write release notes');
	await form.getByRole('button', { name: 'Create skill' }).click();
	await expect(page.getByTestId('latest')).toHaveText('unpublished');

	await page
		.getByRole('textbox', { name: 'SKILL.md' })
		.fill('# Release notes\n\nOne line per change.');
	await page.getByRole('button', { name: 'Save and publish' }).click();
	await expect(page.getByText('Published v1.')).toBeVisible();

	await page
		.getByRole('textbox', { name: 'SKILL.md' })
		.fill('# Release notes\n\nOne line per change.\nLink the ticket.');
	await page.getByRole('button', { name: 'Add file' }).click();
	await page.getByLabel('Path').fill('templates/notes.md');
	await page.getByLabel('Content').fill('- <change> (<ticket>)');
	await page.getByRole('button', { name: 'Save draft' }).click();
	await expect(page.getByLabel('Unpublished changes to SKILL.md')).toContainText(
		'+ Link the ticket.'
	);
	await page.getByRole('button', { name: 'Publish' }).click();
	await expect(page.getByTestId('latest')).toHaveText('v2');

	await page.getByRole('list', { name: 'Versions' }).getByRole('button', { name: 'v2' }).click();
	await expect(page.getByLabel('Changes to templates/notes.md in v2')).toContainText(
		'+ - <change>'
	);
	await page.screenshot({ path: 'test-results/live-skill.png', fullPage: true });

	await page.goto(`/projects/${project}/skills`);
	const version = page.getByLabel('Version of release-notes');
	await expect(version).toHaveValue('latest');
	await version.selectOption('v1');
	await expect(page.getByRole('row').filter({ hasText: 'release-notes' })).toContainText('v2');
	const resolved = await api('GET', `/projects/${project}/skills`);
	expect(resolved.items).toContainEqual(
		expect.objectContaining({ name: 'release-notes', version: 1, pinned: true })
	);
	await version.selectOption('disabled');
	await page.reload();
	await expect(page.getByLabel('Version of release-notes')).toHaveValue('disabled');
	await page.screenshot({ path: 'test-results/live-project-skills.png', fullPage: true });

	expect(errors).toEqual([]);
});
