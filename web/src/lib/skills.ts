import type { Target } from '$lib/permissions.svelte';

/** A skill scope as Core writes it: organization, customer:<key> or project:<key>. */
export interface Scope {
	kind: 'organization' | 'customer' | 'project';
	key: string;
}

export function parseScope(s: string): Scope {
	const [kind, key = ''] = s.split(':', 2);
	if ((kind === 'customer' || kind === 'project') && key) return { kind, key };
	return { kind: 'organization', key: '' };
}

export function formatScope(s: Scope): string {
	return s.kind === 'organization' ? 'organization' : `${s.kind}:${s.key}`;
}

/**
 * The permission target for a scope. A project scope also needs its
 * customer, because customer-wide bindings cover the customer's projects.
 */
export function scopeTarget(s: Scope, projectCustomer?: string): Target {
	if (s.kind === 'customer') return { customer: s.key };
	if (s.kind === 'project') return { customer: projectCustomer, project: s.key };
	return {};
}

/** The parts of a skill version or draft that are compared in diffs. */
export interface SkillContent {
	description: string;
	body: string;
	files: Record<string, string>;
}

/**
 * Content as one path → text map for diffing: the description, SKILL.md
 * (the body) and the supporting files.
 */
export function contentFiles(c: SkillContent): Record<string, string> {
	return { '(description)': c.description, 'SKILL.md': c.body, ...c.files };
}
