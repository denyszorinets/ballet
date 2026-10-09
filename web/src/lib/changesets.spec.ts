import { describe as group, expect, it } from 'vitest';
import { describe, requirements, toggle, type Operation } from './changesets';

const ops: Operation[] = [
	{ kind: 'create_item', ref: 'auth', create: { kind: 'epic', title: 'Auth' } },
	{ kind: 'create_item', ref: 'login', create: { kind: 'ticket', title: 'Login', epic: '$auth' } },
	{ kind: 'add_dependency', dependency: { from: '$login', to: 'WEB-7', type: 'blocks' } },
	{ kind: 'update_item', update: { item: 'WEB-7', title: 'Profile' } }
];

group('changeset operations', () => {
	it('finds what each operation needs', () => {
		expect(requirements(ops)).toEqual([[], [0], [1], []]);
	});

	it('selects requirements and deselects dependents', () => {
		const all = toggle(new Set(), 2, ops);
		expect([...all].sort()).toEqual([0, 1, 2]);
		const without = toggle(new Set([0, 1, 2, 3]), 0, ops);
		expect([...without]).toEqual([3]);
		expect([...toggle(new Set([0, 1, 2]), 2, ops)].sort()).toEqual([0, 1]);
	});

	it('describes operations', () => {
		expect(describe(ops[1], ops)).toBe('Create ticket “Login” in “Auth” (new)');
		expect(describe(ops[2], ops)).toBe('“Login” (new) blocks WEB-7');
		expect(describe(ops[3], ops)).toBe('Update WEB-7 (title): title → “Profile”');
	});

	it('describes feature operations and tracks their refs', () => {
		const fops: Operation[] = [
			{ kind: 'create_feature', ref: 'pdf', feature: { title: 'PDF export', projects: ['WEB'] } },
			{
				kind: 'update_feature',
				feature_update: { feature: 'F-1', description: 'x', status: 'changing' }
			},
			{ kind: 'link_features', feature_link: { from: '$pdf', to: 'F-1', type: 'derived_from' } },
			{
				kind: 'create_item',
				ref: 't',
				create: { kind: 'ticket', title: 'Render', features: ['$pdf', 'F-1'] }
			}
		];
		expect(requirements(fops)).toEqual([[], [], [0], [0]]);
		expect(describe(fops[0], fops)).toBe('Create feature “PDF export” in WEB');
		expect(describe(fops[1], fops)).toBe(
			'Update feature F-1 (description, status): status → changing'
		);
		expect(describe(fops[2], fops)).toBe('“PDF export” (new) derived from F-1');
		expect(describe(fops[3], fops)).toBe(
			'Create ticket “Render”, changing “PDF export” (new), F-1'
		);
	});
});
