import type { Target } from '$lib/permissions.svelte';

/** A skill scope as Core writes it: platform, organization:<key> or project:<key>. */
export interface Scope {
	kind: 'platform' | 'organization' | 'project';
	key: string;
}

export function parseScope(s: string): Scope {
	const [kind, key = ''] = s.split(':', 2);
	if ((kind === 'organization' || kind === 'project') && key) return { kind, key };
	return { kind: 'platform', key: '' };
}

export function formatScope(s: Scope): string {
	return s.kind === 'platform' ? 'platform' : `${s.kind}:${s.key}`;
}

/**
 * The permission target for a scope. A project scope also needs its
 * organization, because organization-wide bindings cover the organization's projects.
 */
export function scopeTarget(s: Scope, projectOrganization?: string): Target {
	if (s.kind === 'organization') return { organization: s.key };
	if (s.kind === 'project') return { organization: projectOrganization, project: s.key };
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
