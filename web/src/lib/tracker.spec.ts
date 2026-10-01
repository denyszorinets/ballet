import { describe, expect, it } from 'vitest';
import { nextStates, upsert, type Item } from './tracker';

describe('nextStates', () => {
	it('follows the human transition table', () => {
		expect(nextStates('ticket', 'backlog')).toEqual(['ready', 'cancelled', 'done']);
		expect(nextStates('ticket', 'in_progress')).toEqual(['paused', 'cancelled']);
		expect(nextStates('epic', 'open')).toEqual(['done', 'cancelled']);
		expect(nextStates('milestone', 'backlog')).toEqual([]);
	});
});

describe('upsert', () => {
	const item = (key: string, title = key) => ({ key, title }) as Item;

	it('replaces by key and keeps numeric order', () => {
		const items = [item('WEB-2'), item('WEB-10')];
		const got = upsert(upsert(items, item('WEB-3')), item('WEB-2', 'renamed'));
		expect(got.map((i) => [i.key, i.title])).toEqual([
			['WEB-2', 'renamed'],
			['WEB-3', 'WEB-3'],
			['WEB-10', 'WEB-10']
		]);
	});
});
