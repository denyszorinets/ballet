import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

const question = (id: string, ticket: string, text: string, extra: object = {}) => ({
	id,
	ticket,
	run: 'r1',
	text,
	blocking: true,
	status: 'open',
	route: 'human',
	created_at: ago(90),
	organization: 'acme',
	project: 'WEB',
	ticket_title: `Title of ${ticket}`,
	ticket_state: 'waiting_for_answer',
	blocked_behind: 0,
	...extra
});

/** Opens the inbox with two open questions; the planner echoes messages. */
async function open(page: Page, role: string): Promise<FakeCore> {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'organization:acme' }],
		organizations: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', organization: 'acme', name: 'Web', description: '', version: 1 }
		],
		inbox: [
			question('q1', 'WEB-2', 'Which identity provider do we use?', {
				context: 'Keycloak or **Okta**.',
				blocked_behind: 3
			}),
			question('q2', 'WEB-5', 'Should errors be logged in JSON?', { blocking: false })
		]
	});
	let watch = '';
	await fakeRealtime(page, async (method, params, notify) => {
		if (method === 'stream.subscribe' || method === 'stream.unsubscribe') return {};
		if (method === 'planner.watch') {
			watch = params.subscription as string;
			return { subscription: watch, running: false };
		}
		if (method === 'planner.send') {
			const session = params.session as string;
			const msgs = core.plannerMessages[session];
			const seq = msgs.length + 1;
			msgs.push({
				seq,
				role: 'user',
				author: 'user-alice',
				content: [{ type: 'text', text: params.text as string }]
			});
			msgs.push({
				seq: seq + 1,
				role: 'assistant',
				content: [{ type: 'text', text: 'Keycloak is in the decisions.' }]
			});
			setTimeout(
				() =>
					notify('planner.output', {
						subscription: watch,
						session,
						type: 'done',
						text: 'end_turn'
					}),
				20
			);
			return { seq };
		}
		throw { code: -32601, message: `fake: no method ${method}` };
	});
	await page.goto('/inbox');
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('engineers answer the most impactful question first and the next one opens', async ({
	page
}) => {
	const core = await open(page, 'engineer');
	const nav = page.getByRole('navigation', { name: 'Main' });
	await expect(nav.getByRole('link', { name: /Inbox/ })).toContainText('2');

	const list = page.getByRole('list', { name: 'Open questions' });
	await expect(list.getByRole('listitem')).toHaveCount(2);
	await expect(list.getByRole('listitem').first()).toContainText('3 waiting behind');
	await expect(list.getByRole('listitem').first()).toContainText('blocking');
	const detail = page.getByRole('region', { name: 'Question' });
	await expect(
		detail.getByRole('heading', { name: 'Which identity provider do we use?' })
	).toBeVisible();
	await expect(detail.locator('.context strong')).toHaveText('Okta');
	await expect(detail.getByRole('link', { name: 'WEB-2 · Title of WEB-2' })).toBeVisible();

	// Discuss it with the planner in a sub-chat.
	await detail.getByRole('button', { name: 'Open a sub-chat' }).click();
	await detail.getByRole('textbox', { name: 'Message' }).fill('What did we decide?');
	await detail.getByRole('textbox', { name: 'Message' }).press('Enter');
	await expect(detail.getByRole('log', { name: 'Conversation' })).toContainText(
		'Keycloak is in the decisions.'
	);

	await detail.getByRole('textbox', { name: 'Your answer' }).fill('Keycloak.');
	await detail.getByRole('button', { name: 'Answer and resume' }).click();
	await expect(list.getByRole('listitem')).toHaveCount(1);
	await expect(
		detail.getByRole('heading', { name: 'Should errors be logged in JSON?' })
	).toBeVisible();
	expect(core.answers).toEqual({ q1: 'Keycloak.' });
});

test('viewers read questions but cannot answer', async ({ page }) => {
	await open(page, 'viewer');
	const detail = page.getByRole('region', { name: 'Question' });
	await expect(
		detail.getByRole('heading', { name: 'Which identity provider do we use?' })
	).toBeVisible();
	await expect(detail.getByText('You can read this question but not answer it.')).toBeVisible();
	await expect(detail.getByRole('button', { name: 'Answer and resume' })).toHaveCount(0);
});

test('an empty inbox says so', async ({ page }) => {
	await fakeOIDC(page);
	await fakeCore(page, { me: [{ role: 'engineer', scope: 'organization:acme' }] });
	await fakeRealtime(page, async () => ({}));
	await page.goto('/inbox');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByText('No open questions.')).toBeVisible();
});
