import { expect, test } from '@playwright/test';

// Needs Core, the LLM gateway and fake-anthropic (bin/fake-anthropic) with
// an Anthropic credential for project WEB of organization acme pointing at it:
// the fake calls a tool when a message reads "/tool <name> <json>".
test('alice plans in chat and approves the planner’s changeset', async ({ page }) => {
	const errors: string[] = [];
	page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
	const title = `Live ${Date.now().toString(36)}`;

	await page.goto('/');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByLabel(/username/i).fill('alice');
	await page
		.getByLabel(/password/i)
		.first()
		.fill('alice');
	await page.getByRole('button', { name: /sign in/i }).click();
	await expect(page.getByRole('heading', { name: 'Organizations' })).toBeVisible();

	await page.goto('/projects/WEB/planner');
	await page.getByLabel('Topic').fill(title);
	await page.getByRole('button', { name: 'Start chat' }).click();
	await expect(page.getByRole('heading', { name: title })).toBeVisible();
	await expect(page.getByText('Live', { exact: true })).toBeVisible();

	const box = page.getByRole('textbox', { name: 'Message' });
	await box.fill('Hello planner');
	await box.press('Enter');
	const log = page.getByRole('log', { name: 'Conversation' });
	await expect(log.getByText('hello', { exact: true })).toBeVisible();

	const op = JSON.stringify({
		title,
		summary: 'From the **live** test.',
		operations: [
			{ kind: 'create_item', ref: 'a', create: { kind: 'ticket', title: `${title} A` } },
			{ kind: 'create_item', ref: 'b', create: { kind: 'ticket', title: `${title} B` } },
			{ kind: 'add_dependency', dependency: { from: '$a', to: '$b', type: 'blocks' } }
		]
	});
	await box.fill(`/tool propose_changeset ${op}`);
	await box.press('Enter');
	const card = page.getByRole('article', { name: `Changeset ${title}` });
	await expect(card.getByTestId('changeset-status')).toHaveText('proposed');
	await expect(log.getByText(/^Tool result:/)).toBeVisible();

	await card.getByLabel(`Create ticket “${title} B”`).uncheck();
	await card.getByRole('button', { name: 'Approve selected (1)' }).click();
	await expect(card.getByTestId('changeset-status')).toHaveText('applied');
	const created = card.getByRole('link', { name: /^WEB-\d+$/ });
	await expect(created).toHaveCount(1);
	await page.screenshot({ path: 'test-results/live-planner.png', fullPage: true });

	await created.click();
	await expect(page.getByRole('heading', { name: `${title} A` })).toBeVisible();
	expect(errors).toEqual([]);
});
