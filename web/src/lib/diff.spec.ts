import { describe, expect, it } from 'vitest';
import { diffFiles, diffLines } from './diff';

describe('diffLines', () => {
	it('marks kept, removed and added lines in order', () => {
		expect(diffLines('a\nb\nc', 'a\nc\nd')).toEqual([
			{ op: ' ', text: 'a' },
			{ op: '-', text: 'b' },
			{ op: ' ', text: 'c' },
			{ op: '+', text: 'd' }
		]);
	});

	it('handles empty sides', () => {
		expect(diffLines('', 'x')).toEqual([{ op: '+', text: 'x' }]);
		expect(diffLines('x', '')).toEqual([{ op: '-', text: 'x' }]);
		expect(diffLines('', '')).toEqual([]);
	});
});

describe('diffFiles', () => {
	it('reports added, removed and changed files, sorted, and skips unchanged ones', () => {
		const d = diffFiles(
			{ 'a.md': '1', 'b.md': 'same', 'c.md': 'x' },
			{ 'a.md': '2', 'b.md': 'same', 'd.md': 'y' }
		);
		expect(d.map((f) => [f.path, f.status])).toEqual([
			['a.md', 'changed'],
			['c.md', 'removed'],
			['d.md', 'added']
		]);
		expect(d[0].lines).toEqual([
			{ op: '-', text: '1' },
			{ op: '+', text: '2' }
		]);
	});
});
