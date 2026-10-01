import { describe, expect, it } from 'vitest';
import { can } from './permissions.svelte';

const roles = {
	'org-admin': ['customer.create', 'customer.update', 'project.create', 'role_binding.manage'],
	'customer-admin': ['customer.update', 'project.create', 'role_binding.manage'],
	viewer: ['customer.read']
};

function binding(role: string, scope: string) {
	return { id: '1', claim: 'groups', value: 'g', role: role as 'viewer', scope, bootstrap: false };
}

describe('can', () => {
	it('org bindings apply everywhere', () => {
		const b = [binding('org-admin', 'organization')];
		expect(can(b, roles, 'customer.create', {})).toBe(true);
		expect(can(b, roles, 'project.create', { customer: 'acme' })).toBe(true);
	});

	it('customer bindings apply to that customer only', () => {
		const b = [binding('customer-admin', 'customer:acme')];
		expect(can(b, roles, 'project.create', { customer: 'acme' })).toBe(true);
		expect(can(b, roles, 'project.create', { customer: 'globex' })).toBe(false);
		expect(can(b, roles, 'customer.create', {})).toBe(false);
	});

	it('project bindings apply to that project', () => {
		const b = [binding('viewer', 'project:WEB')];
		expect(can(b, roles, 'customer.read', { customer: 'acme', project: 'WEB' })).toBe(true);
		expect(can(b, roles, 'customer.read', { customer: 'acme', project: 'APP' })).toBe(false);
	});

	it('requires the role to grant the action', () => {
		expect(can([binding('viewer', 'organization')], roles, 'customer.create', {})).toBe(false);
		expect(can([], roles, 'customer.read', {})).toBe(false);
	});
});
