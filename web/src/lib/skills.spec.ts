import { describe, expect, it } from 'vitest';
import { contentFiles, formatScope, parseScope, scopeTarget } from './skills';

describe('scopes', () => {
	it('parses and formats scope strings', () => {
		expect(parseScope('organization:acme')).toEqual({ kind: 'organization', key: 'acme' });
		expect(parseScope('project:WEB')).toEqual({ kind: 'project', key: 'WEB' });
		expect(parseScope('platform')).toEqual({ kind: 'platform', key: '' });
		expect(parseScope('organization:')).toEqual({ kind: 'platform', key: '' });
		expect(formatScope({ kind: 'project', key: 'WEB' })).toBe('project:WEB');
		expect(formatScope({ kind: 'platform', key: '' })).toBe('platform');
	});

	it('maps scopes to permission targets', () => {
		expect(scopeTarget({ kind: 'platform', key: '' })).toEqual({});
		expect(scopeTarget({ kind: 'organization', key: 'acme' })).toEqual({ organization: 'acme' });
		expect(scopeTarget({ kind: 'project', key: 'WEB' }, 'acme')).toEqual({
			organization: 'acme',
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
