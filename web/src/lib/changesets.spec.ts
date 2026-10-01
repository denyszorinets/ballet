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
});
