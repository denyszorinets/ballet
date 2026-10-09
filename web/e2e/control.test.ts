import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

async function open(
	page: Page,
	me: FakeCore['me'],
	path: string,
	state: Partial<FakeCore> = {}
): Promise<FakeCore> {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me,
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web', description: '', version: 1 }
		],
		...state
	});
	await fakeRealtime(page, async () => ({}));
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('admins pause, resume and kill a project', async ({ page }) => {
	const core = await open(
		page,
		[{ role: 'customer-admin', scope: 'customer:acme' }],
		'/projects/WEB',
		{
			activeRuns: 2
		}
	);
	const control = page.getByRole('region', { name: 'Autonomous work' });
	await expect(control).toContainText('running');

	await control.getByRole('textbox', { name: 'Reason' }).fill('Release freeze');
	await control.getByRole('button', { name: 'Pause' }).click();
	await expect(control.getByRole('status')).toContainText('Paused');
	await expect(control.getByRole('status')).toContainText('Release freeze');
	expect(core.pauses).toHaveLength(1);

	await control.getByRole('button', { name: 'Resume' }).click();
	await expect(control.getByRole('status')).toContainText('running');
	expect(core.pauses).toHaveLength(0);

	page.once('dialog', (d) => d.accept());
	await control.getByRole('button', { name: 'Stop all runs' }).click();
	await expect(control).toContainText('Cancelled 2 runs.');
	await expect(control.getByRole('status')).toContainText('Paused');
});

test('engineers see the state but no controls', async ({ page }) => {
	await open(page, [{ role: 'engineer', scope: 'customer:acme' }], '/projects/WEB', {
		pauses: [
			{ scope: 'platform', paused_by: 'user-alice', paused_at: new Date().toISOString() }
		]
	});
	const control = page.getByRole('region', { name: 'Autonomous work' });
	await expect(control.getByRole('status')).toContainText('all autonomous work is paused');
	await expect(control.getByRole('button')).toHaveCount(0);
});

test('platform admins pause everything from the home page', async ({ page }) => {
	const core = await open(page, [{ role: 'platform-admin', scope: 'platform' }], '/');
	const control = page.getByRole('region', { name: 'Autonomous work' });
	await expect(control).toContainText('the platform is running');
	await control.getByRole('button', { name: 'Pause' }).click();
	await expect(control.getByRole('status')).toContainText('Paused');
	expect(core.pauses[0].scope).toBe('platform');
});
