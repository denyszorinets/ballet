import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeChangeset, type FakeCore } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';
import { fakeRealtime } from './fixtures/realtime';

const plan: FakeChangeset['operations'] = [
	{ kind: 'create_item', ref: 'auth', create: { kind: 'epic', title: 'Auth' } },
	{ kind: 'create_item', ref: 'login', create: { kind: 'ticket', title: 'Login', epic: '$auth' } },
	{
		kind: 'create_item',
		ref: 'logout',
		create: { kind: 'ticket', title: 'Logout', epic: '$auth' }
	},
	{ kind: 'add_dependency', dependency: { from: '$login', to: '$logout', type: 'blocks' } }
];

/**
 * A scripted planner: every message gets a streamed answer, a
 * propose_changeset call creating `plan`, and a closing remark.
 */
async function open(page: Page, role: string, path: string, state: Partial<FakeCore> = {}) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		...state
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
			const add = (m: (typeof msgs)[number]) => {
				msgs.push(m);
				return out('message', { seq: m.seq });
			};
			const out = (type: string, extra: object = {}) =>
				notify('planner.output', { subscription: watch, session, type, ...extra });
			const seq = msgs.length + 1;
			msgs.push({
				seq,
				role: 'user',
				author: 'user-alice',
				content: [{ type: 'text', text: params.text as string }]
			});
			setTimeout(async () => {
				await out('message', { seq });
				await out('text', { text: 'Here is ' });
				await out('text', { text: 'a **plan**.' });
				await add({
					seq: seq + 1,
					role: 'assistant',
					content: [
						{ type: 'text', text: 'Here is a **plan**.' },
						{
							type: 'tool_use',
							tool_use_id: 't1',
							name: 'propose_changeset',
							input: { title: 'Auth' }
						}
					]
				});
				await out('tool_call', { tool: 'propose_changeset', tool_use_id: 't1' });
				const cs: FakeChangeset = {
					id: `cs${seq}`,
					project: 'WEB',
					title: 'Auth',
					summary: 'Users need **accounts**.',
					status: 'proposed',
					operations: plan,
					approved: [],
					results: []
				};
				core.changesets.push(cs);
				await out('tool_result', { tool: 'propose_changeset', text: `{"changeset":"${cs.id}"}` });
				await add({
					seq: seq + 2,
					role: 'user',
					content: [
						{
							type: 'tool_result',
							tool_use_id: 't1',
							text: `{"changeset":"${cs.id}","status":"proposed"}`
						}
					]
				});
				await add({
					seq: seq + 3,
					role: 'assistant',
					content: [{ type: 'text', text: 'Approve it when ready.' }]
				});
				await out('done', { text: 'end_turn' });
			}, 50);
			return { seq };
		}
		throw { code: -32601, message: `fake: no method ${method}` };
	});
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('plan in chat and approve part of the changeset', async ({ page }) => {
	const core = await open(page, 'engineer', '/projects/WEB');
	await page.getByRole('main').getByRole('link', { name: 'Planner' }).click();
	await page.getByLabel('Topic').fill('Authentication');
	await page.getByRole('button', { name: 'Start chat' }).click();
	await expect(page.getByRole('heading', { name: 'Authentication' })).toBeVisible();

	await page.getByRole('textbox', { name: 'Message' }).fill('We need login and logout');
	await page.getByRole('textbox', { name: 'Message' }).press('Enter');

	const log = page.getByRole('log', { name: 'Conversation' });
	await expect(log.getByText('We need login and logout')).toBeVisible();
	await expect(log.locator('strong', { hasText: 'plan' })).toBeVisible();
	await expect(log.getByText('Approve it when ready.')).toBeVisible();
	await expect(log.locator('summary', { hasText: 'propose_changeset' })).toBeVisible();

	const card = page.getByRole('article', { name: 'Changeset Auth' });
	await expect(card.getByTestId('changeset-status')).toHaveText('proposed');
	await expect(card.getByText('accounts')).toBeVisible();
	await expect(card.getByRole('checkbox')).toHaveCount(4);
	await expect(card.getByLabel('“Login” (new) blocks “Logout” (new)')).toBeChecked();

	// Dropping the logout ticket drops the dependency that needs it.
	await card.getByLabel('Create ticket “Logout” in “Auth” (new)').uncheck();
	await expect(card.getByLabel('“Login” (new) blocks “Logout” (new)')).not.toBeChecked();
	await card.getByRole('button', { name: 'Approve selected (2)' }).click();

	await expect(card.getByTestId('changeset-status')).toHaveText('applied');
	await expect(card.getByRole('link', { name: 'WEB-2' })).toBeVisible();
	await expect(card.getByText('not applied')).toHaveCount(2);
	expect(core.items.map((i) => `${i.key} ${i.title} ${i.epic ?? ''}`)).toEqual([
		'WEB-1 Auth ',
		'WEB-2 Login WEB-1'
	]);
	expect(core.changesets[0].approved).toEqual([0, 1]);
});

test('viewers read the chat but cannot write or decide', async ({ page }) => {
	await open(page, 'viewer', '/planner/s1', {
		plannerSessions: [
			{ id: 's1', project: 'WEB', title: 'Billing', created_by: 'bob', running: false }
		],
		plannerMessages: {
			s1: [
				{ seq: 1, role: 'user', author: 'bob', content: [{ type: 'text', text: 'Plan billing' }] },
				{
					seq: 2,
					role: 'assistant',
					content: [{ type: 'tool_use', tool_use_id: 't', name: 'propose_changeset', input: {} }]
				},
				{
					seq: 3,
					role: 'user',
					content: [{ type: 'tool_result', tool_use_id: 't', text: '{"changeset":"cs9"}' }]
				}
			]
		},
		changesets: [
			{
				id: 'cs9',
				project: 'WEB',
				title: 'Billing',
				summary: '',
				status: 'proposed',
				operations: plan,
				approved: [],
				results: []
			}
		]
	});
	await expect(page.getByText('Plan billing')).toBeVisible();
	const card = page.getByRole('article', { name: 'Changeset Billing' });
	await expect(card).toBeVisible();
	await expect(card.getByRole('button')).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'Message' })).toHaveCount(0);
});

test('the changesets page lists proposals and rejects them', async ({ page }) => {
	const core = await open(page, 'engineer', '/projects/WEB/changesets', {
		changesets: [
			{
				id: 'cs1',
				project: 'WEB',
				title: 'Search',
				summary: '',
				status: 'proposed',
				operations: plan,
				approved: [],
				results: []
			},
			{
				id: 'cs2',
				project: 'WEB',
				title: 'Old',
				summary: '',
				status: 'rejected',
				operations: plan,
				approved: [],
				results: []
			}
		]
	});
	const card = page.getByRole('article', { name: 'Changeset Search' });
	await expect(card).toBeVisible();
	await expect(page.getByRole('article', { name: 'Changeset Old' })).toHaveCount(0);
	await card.getByRole('button', { name: 'Reject' }).click();
	await expect(card.getByTestId('changeset-status')).toHaveText('rejected');
	expect(core.changesets[0].status).toBe('rejected');

	await page.getByLabel('Status').selectOption('');
	await expect(page.getByRole('article')).toHaveCount(2);
});
