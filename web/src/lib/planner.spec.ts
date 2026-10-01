import { describe, expect, it } from 'vitest';
import { chatEntries, type PlannerMessage } from './planner';

const usage = { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 };
const at = '2026-10-01T00:00:00Z';

describe('chatEntries', () => {
	it('pairs tool calls with results and finds proposed changesets', () => {
		const msgs: PlannerMessage[] = [
			{
				seq: 1,
				role: 'user',
				author: 'bob',
				content: [{ type: 'text', text: 'Plan login' }],
				usage,
				created_at: at
			},
			{
				seq: 2,
				role: 'assistant',
				content: [
					{ type: 'text', text: 'Proposing.' },
					{ type: 'tool_use', tool_use_id: 't1', name: 'propose_changeset', input: { title: 'x' } }
				],
				usage,
				created_at: at
			},
			{
				seq: 3,
				role: 'user',
				content: [
					{
						type: 'tool_result',
						tool_use_id: 't1',
						text: '{"changeset":"cs1","status":"proposed"}'
					}
				],
				usage,
				created_at: at
			},
			{
				seq: 4,
				role: 'assistant',
				content: [{ type: 'text', text: 'Done.' }],
				usage,
				created_at: at
			}
		];
		const entries = chatEntries(msgs);
		expect(entries.map((e) => e.kind)).toEqual(['human', 'assistant', 'tool', 'assistant']);
		const tool = entries[2];
		expect(tool.kind === 'tool' && tool.call).toMatchObject({
			name: 'propose_changeset',
			changeset: 'cs1',
			isError: false
		});
	});
});
