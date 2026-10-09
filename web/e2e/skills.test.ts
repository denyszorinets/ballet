import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeSkill } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

const gitflow: FakeSkill = {
	id: 's1',
	scope: 'platform',
	name: 'gitflow',
	description: 'Branching rules',
	body: '# Gitflow\n\nBranch from develop.',
	files: {},
	version: 3,
	versions: [
		{ description: 'Branching rules', body: '# Gitflow\n\nBranch from main.', files: {} },
		{ description: 'Branching rules', body: '# Gitflow\n\nBranch from develop.', files: {} }
	]
};
const review: FakeSkill = {
	id: 's2',
	scope: 'customer:acme',
	name: 'code-review',
	description: 'Review checklist',
	body: 'Check tests.',
	files: {},
	version: 2,
	versions: [{ description: 'Review checklist', body: 'Check tests.', files: {} }]
};

async function open(page: Page, me: { role: string; scope: string }[], path: string) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me,
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		skills: [structuredClone(gitflow), structuredClone(review)]
	});
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

const platformAdmin = [{ role: 'platform-admin', scope: 'platform' }];

test('skills are listed per scope and created', async ({ page }) => {
	await open(page, platformAdmin, '/skills');

	await expect(page.getByRole('link', { name: 'gitflow' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'code-review' })).toHaveCount(0);

	await page.getByRole('combobox', { name: /^Scope/ }).selectOption('customer');
	await page.getByLabel('Customer key').fill('acme');
	await page.getByRole('button', { name: 'Show' }).click();
	await expect(page).toHaveURL(/scope=customer%3Aacme/);
	await expect(page.getByRole('link', { name: 'code-review' })).toBeVisible();

	const form = page.getByRole('form', { name: 'New skill' });
	await form.getByLabel('Name').fill('testing');
	await form.getByLabel('Description').fill('How we test');
	await form.getByRole('button', { name: 'Create skill' }).click();
	await expect(page.getByRole('heading', { name: 'testing' })).toBeVisible();
	await expect(page.getByTestId('latest')).toHaveText('unpublished');
});

test('a customer engineer sees their customer scope and cannot edit', async ({ page }) => {
	await open(page, [{ role: 'engineer', scope: 'customer:acme' }], '/skills');
	await expect(page.getByRole('heading', { name: 'customer:acme' })).toBeVisible();
	await page.getByRole('link', { name: 'code-review' }).click();
	await expect(page.getByText('Check tests.')).toBeVisible();
	await expect(page.getByRole('form', { name: 'Skill draft' })).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'New skill' })).toHaveCount(0);
});

test('editing, publishing and comparing versions', async ({ page }) => {
	const core = await open(page, platformAdmin, '/skills/s1');

	await expect(page.getByTestId('latest')).toHaveText('v2');
	await expect(page.getByText('Unpublished changes')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Publish' })).toBeDisabled();

	await page
		.getByRole('textbox', { name: 'SKILL.md' })
		.fill('# Gitflow\n\nBranch from develop.\nMerge with squash.');
	await page.getByRole('button', { name: 'Add file' }).click();
	await page.getByLabel('Path').fill('references/naming.md');
	await page.getByLabel('Content').fill('feature/<issue>_<slug>');
	await page.getByRole('button', { name: 'Save draft' }).click();
	await expect(page.getByText('Draft saved.')).toBeVisible();

	const changes = page.getByLabel('Unpublished changes to SKILL.md');
	await expect(changes).toContainText('+ Merge with squash.');
	await expect(page.getByLabel('Unpublished changes to references/naming.md')).toContainText(
		'+ feature/<issue>_<slug>'
	);

	await page.getByRole('button', { name: 'Publish' }).click();
	await expect(page.getByText('Published v3.')).toBeVisible();
	await expect(page.getByTestId('latest')).toHaveText('v3');
	await expect(page.getByText('Unpublished changes')).toHaveCount(0);
	expect(core.skills[0].versions).toHaveLength(3);
	expect(core.skills[0].versions[2].files).toEqual({
		'references/naming.md': 'feature/<issue>_<slug>'
	});

	const versions = page.getByRole('list', { name: 'Versions' });
	await expect(versions.getByRole('listitem')).toHaveCount(3);
	await versions.getByRole('button', { name: 'v2' }).click();
	const v2 = page.getByLabel('Changes to SKILL.md in v2');
	await expect(v2).toContainText('- Branch from main.');
	await expect(v2).toContainText('+ Branch from develop.');

	// Editing without saving offers "Save and publish".
	await page.getByLabel('Description').fill('Branching and merging rules');
	await page.getByRole('button', { name: 'Save and publish' }).click();
	await expect(page.getByText('Published v4.')).toBeVisible();
	expect(core.skills[0].versions[3].description).toBe('Branching and merging rules');
});

test('project skills: pin a version, disable and re-enable', async ({ page }) => {
	const core = await open(
		page,
		[{ role: 'customer-admin', scope: 'customer:acme' }],
		'/projects/WEB'
	);
	await page.getByRole('main').getByRole('link', { name: 'Skills' }).click();

	const row = (name: string) => page.getByRole('row').filter({ hasText: name });
	await expect(row('gitflow')).toContainText('platform');
	await expect(row('code-review')).toContainText('customer:acme');

	await page.getByLabel('Version of gitflow').selectOption('v1');
	await expect(page.getByLabel('Version of gitflow')).toHaveValue('v1');
	expect(core.pins).toEqual([{ project: 'WEB', name: 'gitflow', version: 1, disabled: false }]);

	await page.getByLabel('Version of code-review').selectOption('disabled');
	await expect(page.getByLabel('Version of code-review')).toHaveValue('disabled');

	await page.reload();
	await expect(page.getByLabel('Version of code-review')).toHaveValue('disabled');
	await page.getByLabel('Version of code-review').selectOption('latest');
	await expect(page.getByLabel('Version of code-review')).toHaveValue('latest');
	expect(core.pins.map((p) => p.name)).toEqual(['gitflow']);
});
