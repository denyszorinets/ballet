import type { ApiClient, Schemas } from '$lib/api/client';

/** Where an action applies: customer and project keys; empty = organization. */
export interface Target {
	customer?: string;
	project?: string;
}

type Binding = Schemas['RoleBinding'];

/**
 * Whether bindings grant action on target, given the server's role →
 * actions table. Mirrors Core's scope rules so the UI can hide actions the
 * caller cannot perform; Core enforces them regardless.
 */
export function can(
	bindings: Binding[],
	roleActions: Record<string, string[]>,
	action: string,
	target: Target
): boolean {
	return bindings.some((b) => {
		if (!roleActions[b.role]?.includes(action)) return false;
		if (b.scope === 'organization') return true;
		const [kind, key] = b.scope.split(':', 2);
		if (kind === 'customer') return target.customer === key;
		if (kind === 'project') return target.project === key;
		return false;
	});
}

/** The caller's role bindings and the role table, loaded after sign-in. */
export class Permissions {
	bindings = $state<Binding[]>([]);
	roles = $state<Record<string, string[]>>({});
	loaded = $state(false);

	async load(api: ApiClient): Promise<void> {
		const [me, roles] = await Promise.all([api.GET('/api/v1/me'), api.GET('/api/v1/roles')]);
		this.bindings = me.data?.bindings ?? [];
		this.roles = Object.fromEntries((roles.data?.items ?? []).map((r) => [r.role, r.actions]));
		this.loaded = true;
	}

	can(action: string, target: Target = {}): boolean {
		return can(this.bindings, this.roles, action, target);
	}

	/** Whether the caller administers access anywhere. */
	get managesAccess(): boolean {
		return this.bindings.some((b) => this.roles[b.role]?.includes('role_binding.manage'));
	}
}
