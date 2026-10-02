import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

const assumption = (id: string, text: string) => ({
	id,
	run: 'r1',
	kind: 'assumption',
	text,
	detail: 'Common default.',
	created_at: new Date().toISOString(),
	ticket: 'WEB-3',
	ticket_title: 'Login'
});

async function open(page: Page, role: string): Promise<FakeCore> {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web', description: '', version: 1 }
		],
		assumptions: [
			assumption('a1', 'Sessions last 8 hours.'),
			assumption('a2', 'UI is English only.')
		]
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto('/projects/WEB/assumptions');
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('engineers confirm and reject assumptions; a rejection creates follow-up work', async ({
	page
}) => {
	const core = await open(page, 'engineer');
	const list = page.getByRole('list', { name: 'Assumptions' });
	await expect(list.getByRole('listitem')).toHaveCount(2);

	const first = list.getByRole('listitem').filter({ hasText: 'Sessions last 8 hours.' });
	await first.getByRole('button', { name: 'Confirm' }).click();
	await expect(list.getByRole('listitem')).toHaveCount(1, { timeout: 5000 });

	const second = list.getByRole('listitem').filter({ hasText: 'UI is English only.' });
	await second.getByRole('button', { name: 'Reject…' }).click();
	await expect(second.getByRole('button', { name: 'Reject', exact: true })).toBeDisabled();
	await second.getByRole('textbox', { name: 'What is right instead?' }).fill('German too.');
	await second.getByRole('button', { name: 'Reject', exact: true }).click();
	await expect(page.getByText('No assumptions.')).toBeVisible();

	await page.getByRole('combobox', { name: 'Review' }).selectOption('rejected');
	const rejected = page.getByRole('list', { name: 'Assumptions' }).getByRole('listitem');
	await expect(rejected).toContainText('German too.');
	await expect(
		rejected.getByRole('link', { name: 'correction proposed as a changeset' })
	).toBeVisible();
	expect(core.assumptions.map((a) => a.review)).toEqual(['confirmed', 'rejected']);
});

test('viewers see the register without review controls', async ({ page }) => {
	await open(page, 'viewer');
	const list = page.getByRole('list', { name: 'Assumptions' });
	await expect(list.getByRole('listitem')).toHaveCount(2);
	await expect(page.getByRole('button', { name: 'Confirm' })).toHaveCount(0);
});
