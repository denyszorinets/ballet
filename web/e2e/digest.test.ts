import { expect, test } from '@playwright/test';
import { fakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

test('the digest summarizes a period and switches periods', async ({ page }) => {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role: 'viewer', scope: 'organization:acme' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', organization: 'acme', name: 'Web', description: '', version: 1 }
		],
		digest: {
			project: 'WEB',
			since: '2026-10-01T22:00:00Z',
			until: '2026-10-02T06:00:00Z',
			done: [{ key: 'WEB-3', title: 'Login', at: '2026-10-02T02:00:00Z' }],
			failed: [],
			started: [],
			merged: [],
			waiting: [],
			questions_raised: 3,
			answered_by_planner: 2,
			answered_by_human: 0,
			open_questions: [
				{ ticket: 'WEB-4', text: 'Which IdP?', blocking: true, since: '2026-10-02T03:00:00Z' }
			],
			assumptions: [],
			proposals: 1,
			runs: { succeeded: 6, failed: 1 },
			tokens: 123456,
			interventions: [],
			markdown: '# Digest of WEB\n\n## Done\n\n- WEB-3 Login\n'
		}
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/projects/WEB/digest');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const summary = page.getByRole('list', { name: 'Summary' });
	await expect(summary).toContainText('1 done');
	await expect(summary).toContainText('7 agent sessions');
	await expect(summary).toContainText('123,456 tokens');
	await expect(summary).toContainText('3 questions (2 by the planner');
	await expect(
		page.getByRole('article', { name: 'Digest' }).getByRole('heading', { name: 'Done' })
	).toBeVisible();
	await expect(page.getByRole('link', { name: /Answer the open questions/ })).toBeVisible();

	await page.getByRole('combobox', { name: 'Period' }).selectOption({ label: 'Last 7 days' });
	await expect.poll(() => core.digestQueries.length).toBe(2);
	const days = (Date.now() - new Date(core.digestQueries[1]).getTime()) / 86400_000;
	expect(Math.round(days)).toBe(7);
});
