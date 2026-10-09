import type { Schemas } from '$lib/api/client';

export type Changeset = Schemas['Changeset'];
export type Operation = Schemas['ChangesetOperation'];

/** Item references an operation uses (keys or "$ref"). */
function references(op: Operation): string[] {
	const refs: (string | undefined)[] = [];
	if (op.create) refs.push(op.create.epic, op.create.milestone, ...(op.create.features ?? []));
	if (op.update) refs.push(op.update.epic, op.update.milestone, ...(op.update.features ?? []));
	if (op.dependency) refs.push(op.dependency.from, op.dependency.to);
	if (op.feature_link) refs.push(op.feature_link.from, op.feature_link.to);
	return refs.filter((r): r is string => !!r);
}

/** For each operation, the indices of the create operations it refers to through "$ref". */
export function requirements(ops: Operation[]): number[][] {
	const declared = new Map<string, number>();
	ops.forEach((op, i) => {
		if ((op.kind === 'create_item' || op.kind === 'create_feature') && op.ref)
			declared.set(op.ref, i);
	});
	return ops.map((op) =>
		references(op)
			.filter((r) => r.startsWith('$'))
			.map((r) => declared.get(r.slice(1)))
			.filter((i): i is number => i !== undefined)
	);
}

/**
 * Toggles operation i in the selection, keeping it consistent: selecting
 * an operation selects what it needs; deselecting one deselects what needs it.
 */
export function toggle(selected: Set<number>, i: number, ops: Operation[]): Set<number> {
	const req = requirements(ops);
	const next = new Set(selected);
	if (next.has(i)) {
		const drop = [i];
		while (drop.length) {
			const j = drop.pop()!;
			if (!next.delete(j)) continue;
			req.forEach((r, k) => {
				if (r.includes(j)) drop.push(k);
			});
		}
	} else {
		const add = [i];
		while (add.length) {
			const j = add.pop()!;
			if (next.has(j)) continue;
			next.add(j);
			add.push(...req[j]);
		}
	}
	return next;
}

/** A reference as people read it: "$login" → "“Login” (new)". */
export function refLabel(ref: string, ops: Operation[]): string {
	if (!ref.startsWith('$')) return ref;
	const op = ops.find(
		(o) => (o.kind === 'create_item' || o.kind === 'create_feature') && o.ref === ref.slice(1)
	);
	const title = op?.create?.title ?? op?.feature?.title;
	return title !== undefined ? `“${title}” (new)` : ref;
}

const linkVerbs: Record<string, string> = {
	derived_from: 'derived from',
	split_from: 'split from',
	merged_into: 'merged into',
	supersedes: 'supersedes',
	depends_on: 'depends on',
	relates: 'relates to'
};

function changesFeatures(features: string[] | undefined, ops: Operation[]): string {
	if (!features?.length) return '';
	return `, changing ${features.map((f) => refLabel(f, ops)).join(', ')}`;
}

/** One line describing an operation. */
export function describe(op: Operation, ops: Operation[]): string {
	if (op.create) {
		const c = op.create;
		const parts = [`Create ${c.kind} “${c.title}”`];
		if (c.epic) parts.push(`in ${refLabel(c.epic, ops)}`);
		if (c.milestone) parts.push(`for ${refLabel(c.milestone, ops)}`);
		return parts.join(' ') + changesFeatures(c.features, ops);
	}
	if (op.update) {
		const u = op.update;
		const fields = (
			[
				'title',
				'description',
				'type',
				'acceptance_criteria',
				'policy',
				'epic',
				'milestone',
				'features'
			] as const
		).filter((f) => u[f] !== undefined);
		const title = u.title !== undefined ? `: title → “${u.title}”` : '';
		return `Update ${u.item} (${fields.join(', ')})${title}`;
	}
	if (op.dependency) {
		const d = op.dependency;
		const verb = d.type === 'blocks' ? 'blocks' : 'relates to';
		return `${refLabel(d.from, ops)} ${verb} ${refLabel(d.to, ops)}`;
	}
	if (op.feature) {
		const projects = op.feature.projects?.length ? ` in ${op.feature.projects.join(', ')}` : '';
		return `Create feature “${op.feature.title}”${projects}`;
	}
	if (op.feature_update) {
		const u = op.feature_update;
		const fields = (['title', 'description', 'status', 'projects'] as const).filter(
			(f) => u[f] !== undefined
		);
		const status = u.status !== undefined ? `: status → ${u.status.replace('_', ' ')}` : '';
		return `Update feature ${u.feature} (${fields.join(', ')})${status}`;
	}
	if (op.feature_link) {
		const l = op.feature_link;
		return `${refLabel(l.from, ops)} ${linkVerbs[l.type] ?? l.type} ${refLabel(l.to, ops)}`;
	}
	return op.kind;
}
