import { describe, expect, it } from 'vitest';
import { transcript } from './sessionlog';

const log = (seq: number, stream: string, text: string) => ({
	seq,
	stream: stream as 'stdout' | 'stderr' | 'system' | 'event',
	text,
	at: '2026-10-08T10:00:00Z'
});

describe('transcript', () => {
	it('turns session events into entries', () => {
		const t = transcript([
			log(
				1,
				'event',
				'{"kind":"text","text":"Looking"}\n{"kind":"tool_use","tool":"Bash","input":"{\\"command\\":\\"ls\\"}"}\n'
			),
			log(2, 'event', '{"kind":"tool_result","text":"a.go","error":true}\n'),
			log(3, 'event', '{"kind":"result","text":"Done."}\n{"kind":"user","text":"More tests"}\n')
		]);
		expect(t).toEqual([
			{ kind: 'text', text: 'Looking' },
			{ kind: 'tool_use', tool: 'Bash', input: 'command: ls' },
			{ kind: 'tool_result', text: 'a.go', error: true },
			{ kind: 'result', text: 'Done.', error: false },
			{ kind: 'user', text: 'More tests' }
		]);
	});

	it('merges consecutive output of a stream and keeps it apart from events', () => {
		const t = transcript([
			log(1, 'system', 'agent: workspace /w\n'),
			log(2, 'stderr', 'cloning\n'),
			log(3, 'stderr', 'on branch x\n'),
			log(4, 'event', 'not json\n{"kind":"text","text":"Hi"}\n')
		]);
		expect(t).toEqual([
			{ kind: 'output', stream: 'system', text: 'agent: workspace /w\n' },
			{ kind: 'output', stream: 'stderr', text: 'cloning\non branch x\n' },
			{ kind: 'text', text: 'Hi' }
		]);
	});

	it('shortens tool input', () => {
		const long = JSON.stringify({ file_path: 'a.go', content: 'x'.repeat(500) });
		const [e] = transcript([
			log(1, 'event', JSON.stringify({ kind: 'tool_use', tool: 'Write', input: long }) + '\n')
		]);
		expect(e.kind).toBe('tool_use');
		if (e.kind === 'tool_use') {
			expect(e.input.startsWith('file_path: a.go, content: xxx')).toBe(true);
			expect(e.input.length).toBeLessThanOrEqual(201);
		}
	});
});
