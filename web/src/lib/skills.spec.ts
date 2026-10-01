import { describe, expect, it } from 'vitest';
import { contentFiles, formatScope, parseScope, scopeTarget } from './skills';

describe('scopes', () => {
	it('parses and formats scope strings', () => {
		expect(parseScope('customer:acme')).toEqual({ kind: 'customer', key: 'acme' });
		expect(parseScope('project:WEB')).toEqual({ kind: 'project', key: 'WEB' });
		expect(parseScope('organization')).toEqual({ kind: 'organization', key: '' });
		expect(parseScope('customer:')).toEqual({ kind: 'organization', key: '' });
		expect(formatScope({ kind: 'project', key: 'WEB' })).toBe('project:WEB');
		expect(formatScope({ kind: 'organization', key: '' })).toBe('organization');
	});

	it('maps scopes to permission targets', () => {
		expect(scopeTarget({ kind: 'organization', key: '' })).toEqual({});
		expect(scopeTarget({ kind: 'customer', key: 'acme' })).toEqual({ customer: 'acme' });
		expect(scopeTarget({ kind: 'project', key: 'WEB' }, 'acme')).toEqual({
			customer: 'acme',
			project: 'WEB'
		});
	});
});

describe('contentFiles', () => {
	it('puts description and body next to the files', () => {
		expect(contentFiles({ description: 'd', body: 'b', files: { 'ref/x.md': 'x' } })).toEqual({
			'(description)': 'd',
			'SKILL.md': 'b',
			'ref/x.md': 'x'
		});
	});
});
