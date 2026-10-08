import { expect, test, type Page } from '@playwright/test';
import { fakeCore, type FakeItem } from './fixtures/api';
import { fakeOIDC } from './fixtures/oidc';

function ticket(n: number, state: string, title: string): FakeItem {
	return {
		id: `t${n}`,
		key: `WEB-${n}`,
		project: 'WEB',
		kind: 'ticket',
		title,
		description: '',
		state,
		type: 'feature',
		acceptance_criteria: [],
		policy: { review_mode: 'agent', merge_mode: 'auto' },
		version: 1
	};
}

async function open(page: Page, role: string, path: string) {
	await fakeOIDC(page);
	const core = await fakeCore(page, {
		me: [{ role, scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		items: [ticket(1, 'ready', 'Schema'), ticket(2, 'ready', 'API'), ticket(3, 'done', 'Setup')],
		deps: [{ id: 'd1', from: 'WEB-1', to: 'WEB-2', type: 'blocks' }]
	});
	await page.goto(path);
	await page.getByRole('button', { name: 'Sign in' }).click();
	return core;
}

test('the board groups tickets by state and marks blocked ones', async ({ page }) => {
	await open(page, 'engineer', '/projects/WEB');

	const ready = page.getByRole('listitem', { name: 'Ready' });
	await expect(ready.getByRole('link')).toHaveCount(2);
	await expect(ready.getByRole('link', { name: /API/ })).toContainText('Blocked');
	await expect(ready.getByRole('link', { name: /Schema/ })).not.toContainText('Blocked');
	await expect(page.getByRole('listitem', { name: 'Done' }).getByRole('link')).toHaveCount(1);

	await page.getByLabel('New ticket').fill('Payments');
	await page.getByRole('button', { name: 'Add' }).click();
	await expect(
		page.getByRole('listitem', { name: 'Backlog' }).getByRole('link', { name: /Payments/ })
	).toBeVisible();
});

test('the item page edits, transitions and manages dependencies', async ({ page }) => {
	await open(page, 'engineer', '/items/WEB-2');

	await expect(page.getByRole('heading', { name: 'API' })).toBeVisible();
	await expect(page.getByRole('listitem').filter({ hasText: 'Blocked by' })).toContainText('WEB-1');

	await page.getByRole('button', { name: 'Edit' }).click();
	await page.getByLabel('Acceptance criteria (one per line)').fill('Returns 200\n\nDocumented');
	await page.getByLabel('Review').selectOption('agent+human');
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByText('Documented')).toBeVisible();
	await expect(page.getByText('Review agent+human')).toBeVisible();

	await page.getByRole('button', { name: 'Move to Paused' }).click();
	await expect(page.getByTestId('state')).toHaveText('Paused');

	await page.getByRole('button', { name: 'Remove dependency on WEB-1' }).click();
	await expect(page.getByText('No dependencies.')).toBeVisible();
	await page.getByLabel('Item key').fill('WEB-3');
	await page.getByLabel('Relation').selectOption('relates');
	await page.getByRole('button', { name: 'Add dependency' }).click();
	await expect(page.getByRole('listitem').filter({ hasText: 'Relates to' })).toContainText('WEB-3');

	await page.getByLabel('Item key').fill('WEB-99');
	await page.getByRole('button', { name: 'Add dependency' }).click();
	await expect(page.getByRole('alert')).toContainText('WEB-99 does not exist');
});

test('viewers get a read-only board and item page', async ({ page }) => {
	await open(page, 'viewer', '/projects/WEB');

	await expect(page.getByRole('listitem', { name: 'Ready' }).getByRole('link')).toHaveCount(2);
	await expect(page.getByLabel('New ticket')).toHaveCount(0);
	await page.getByRole('link', { name: /API/ }).click();
	await expect(page.getByRole('heading', { name: 'API' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: /Move to/ })).toHaveCount(0);
	await expect(page.getByRole('form', { name: 'Add dependency' })).toHaveCount(0);
});

test('the board fits a phone screen without page-level horizontal scrolling', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await open(page, 'engineer', '/projects/WEB');
	await expect(page.getByRole('listitem', { name: 'Ready' })).toBeVisible();

	const overflow = await page.evaluate(
		() => document.documentElement.scrollWidth - window.innerWidth
	);
	expect(overflow).toBeLessThanOrEqual(0);
});

test('the item page shows agent activity', async ({ page }) => {
	await fakeOIDC(page);
	const at = '2026-10-01T10:00:00Z';
	await fakeCore(page, {
		me: [{ role: 'viewer', scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		items: [ticket(1, 'in_progress', 'Login')],
		activity: {
			'WEB-1': {
				runs: [
					{
						id: 'r1',
						project: 'WEB',
						ticket: 'WEB-1',
						stage: 'implement',
						status: 'succeeded',
						spec: { command: ['x'] },
						adapter: 'claude-code',
						result: { summary: 'Login works.', turns: 4, cost_usd: 0.3 },
						created_by: 'alice',
						created_at: at,
						started_at: at,
						version: 3
					}
				],
				reports: [
					{
						id: 'p1',
						run: 'r1',
						kind: 'assumption',
						text: 'Sessions last 8 hours.',
						detail: 'Common default.',
						created_at: at
					},
					{
						id: 'p2',
						run: 'r1',
						kind: 'stage_report',
						outcome: 'done',
						text: 'Implemented login.',
						created_at: at
					}
				],
				questions: [
					{
						id: 'q1',
						run: 'r1',
						text: 'Which IdP for staff?',
						context: 'Keycloak or Entra.',
						blocking: true,
						status: 'open',
						created_at: at
					}
				],
				usage: [
					{
						key: 'run:r1',
						requests: 7,
						input_tokens: 1200,
						output_tokens: 300,
						cache_read_tokens: 5000,
						cache_write_tokens: 100
					}
				],
				history: [
					{
						seq: 2,
						type: 'flow.waiting',
						occurred_at: '2026-10-01T10:05:00Z',
						actor: { kind: 'service', subject: 'ballet' },
						payload: { stage: 'implement', for: 'answer' }
					}
				]
			}
		},
		runLogs: {
			r1: [
				{ seq: 1, stream: 'stderr', text: 'ballet: on branch ballet/WEB-1-login\n', at },
				{
					seq: 2,
					stream: 'event',
					text:
						'{"kind":"text","text":"Adding the login form."}\n' +
						'{"kind":"tool_use","tool":"Bash","input":"{\\"command\\":\\"go test ./...\\"}"}\n' +
						'{"kind":"tool_result","text":"ok"}\n' +
						'{"kind":"result","text":"Login works."}\n',
					at
				}
			]
		}
	});
	await page.goto('/items/WEB-1');
	await page.getByRole('button', { name: 'Sign in' }).click();

	await expect(page.getByRole('heading', { name: 'Agent activity' })).toBeVisible();
	await expect(page.getByRole('list', { name: 'Open questions' })).toContainText(
		'Blocking question: Which IdP for staff?'
	);
	const timeline = page.getByRole('region', { name: 'Timeline' });
	const session = timeline.getByRole('listitem', { name: 'Session implement' });
	await expect(session).toContainText('succeeded');
	await expect(session).toContainText('outcome: done');
	await expect(session).toContainText('claude-code');
	await expect(session).toContainText('Implemented login.');
	await expect(session).toContainText(
		'1,200 in · 300 out · 100 cache write · 5,000 cache read · 7 requests'
	);
	await expect(session).toContainText('Sessions last 8 hours.');
	await expect(session).toContainText('Which IdP for staff?');
	await session.getByRole('button', { name: 'Show session' }).click();
	const transcript = session.getByRole('region', { name: 'Session transcript' });
	await expect(transcript).toContainText('ballet: on branch ballet/WEB-1-login');
	await expect(transcript).toContainText('Adding the login form.');
	await expect(transcript).toContainText('Bash command: go test ./...');
	await expect(transcript).toContainText('Turn ended: Login works.');
	await expect(timeline).toContainText('Waited for answers at implement');
	await expect(timeline).toContainText('1,600 counted tokens');
	const reports = page.getByRole('list', { name: 'Agent reports' });
	await expect(reports.getByRole('listitem').first()).toContainText('stage report: done');
	await expect(reports).toContainText('Sessions last 8 hours.');
});

test('admins talk to a running agent session', async ({ page }) => {
	await fakeOIDC(page);
	const at = '2026-10-01T10:00:00Z';
	const core = await fakeCore(page, {
		me: [{ role: 'customer-admin', scope: 'customer:acme' }],
		customers: [{ id: 'c1', key: 'acme', name: 'Acme', version: 1 }],
		projects: [
			{ id: 'p1', key: 'WEB', customer: 'acme', name: 'Web shop', description: '', version: 1 }
		],
		items: [ticket(1, 'in_progress', 'Login')],
		activity: {
			'WEB-1': {
				runs: [
					{
						id: 'r1',
						project: 'WEB',
						ticket: 'WEB-1',
						stage: 'implement',
						status: 'running',
						spec: { command: [] },
						adapter: 'claude-code',
						created_by: 'ballet',
						created_at: at,
						started_at: at,
						version: 2
					}
				]
			}
		},
		runLogs: {
			r1: [{ seq: 1, stream: 'event', text: '{"kind":"text","text":"Writing the form."}\n', at }]
		}
	});
	await page.goto('/items/WEB-1');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const session = page.getByRole('listitem', { name: 'Session implement' });
	await session.getByRole('button', { name: 'Show session' }).click();
	const transcript = session.getByRole('region', { name: 'Session transcript' });
	await expect(transcript).toContainText('Writing the form.');

	await transcript.getByLabel('Message to the agent').fill('Use the design system buttons.');
	await transcript.getByRole('button', { name: 'Send', exact: true }).click();
	await expect(transcript.getByRole('status')).toContainText('Sent');
	expect(core.runInputs.r1).toEqual([{ kind: 'message', text: 'Use the design system buttons.' }]);

	// The agent delivers it when the turn ends; the transcript follows live.
	core.runLogs.r1.push({
		seq: 2,
		stream: 'event',
		text: '{"kind":"user","text":"Use the design system buttons."}\n',
		at
	});
	await expect(transcript).toContainText('You: Use the design system buttons.');

	await transcript.getByRole('button', { name: 'Interrupt' }).click();
	await expect(transcript.getByRole('status')).toContainText('Interrupted.');
	expect(core.runInputs.r1.at(-1)).toEqual({ kind: 'interrupt' });
});

test('engineers open and merge the pull request of a ticket', async ({ page }) => {
	const core = await open(page, 'engineer', '/items/WEB-2');
	await expect(page.getByText('No pull request yet.')).toBeVisible();
	await page.getByRole('button', { name: 'Open pull request' }).click();
	const pr = page.getByTestId('pull-request');
	await expect(pr.getByRole('link', { name: '#7' })).toHaveAttribute(
		'href',
		'https://github.com/acme/web/pull/7'
	);
	await expect(pr).toContainText('checks: success');
	await expect(pr).toContainText('review: approved');
	await page.getByRole('button', { name: 'Merge' }).click();
	await expect(pr).toContainText('merged');
	await expect(page.getByRole('button', { name: 'Merge' })).toHaveCount(0);
	expect(core.pullRequests['WEB-2'].state).toBe('merged');
});

test('admins start a pipeline and humans decide human stages', async ({ page }) => {
	const core = await open(page, 'customer-admin', '/items/WEB-1');
	await expect(page.getByText('Not started.')).toBeVisible();
	await page.getByRole('button', { name: 'Start pipeline' }).click();
	const stages = page.getByRole('list', { name: 'Pipeline stages' });
	await expect(stages.getByRole('listitem')).toHaveCount(2);
	await expect(page.getByTestId('flow-status')).toContainText('running');

	core.flows['WEB-1'].stage = 'approve';
	core.flows['WEB-1'].status = 'waiting';
	core.flows['WEB-1'].waiting = 'approval';
	await page.reload();
	await expect(page.getByTestId('flow-status')).toContainText('waiting for approval');
	await page.getByRole('button', { name: 'Approve stage' }).click();
	await expect(page.getByTestId('flow-status')).toContainText('done');
});
