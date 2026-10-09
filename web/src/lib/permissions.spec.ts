import { describe, expect, it } from 'vitest';
import { can } from './permissions.svelte';

const roles = {
	'platform-admin': [
		'organization.create',
		'organization.update',
		'project.create',
		'role_binding.manage'
	],
	'organization-admin': ['organization.update', 'project.create', 'role_binding.manage'],
	viewer: ['organization.read']
};

function binding(role: string, scope: string) {
	return { id: '1', claim: 'groups', value: 'g', role: role as 'viewer', scope, bootstrap: false };
}

describe('can', () => {
	it('platform bindings apply everywhere', () => {
		const b = [binding('platform-admin', 'platform')];
		expect(can(b, roles, 'organization.create', {})).toBe(true);
		expect(can(b, roles, 'project.create', { organization: 'acme' })).toBe(true);
	});

	it('organization bindings apply to that organization only', () => {
		const b = [binding('organization-admin', 'organization:acme')];
		expect(can(b, roles, 'project.create', { organization: 'acme' })).toBe(true);
		expect(can(b, roles, 'project.create', { organization: 'globex' })).toBe(false);
		expect(can(b, roles, 'organization.create', {})).toBe(false);
	});

	it('project bindings apply to that project', () => {
		const b = [binding('viewer', 'project:WEB')];
		expect(can(b, roles, 'organization.read', { organization: 'acme', project: 'WEB' })).toBe(true);
		expect(can(b, roles, 'organization.read', { organization: 'acme', project: 'APP' })).toBe(
			false
		);
	});

	it('requires the role to grant the action', () => {
		expect(can([binding('viewer', 'platform')], roles, 'organization.create', {})).toBe(false);
		expect(can([], roles, 'organization.read', {})).toBe(false);
	});
});
