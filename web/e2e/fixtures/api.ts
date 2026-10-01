import type { Page, Route } from '@playwright/test';

/** In-memory fake of Core's REST API for e2e tests. */
export interface FakeCore {
	customers: { id: string; key: string; name: string; version: number }[];
	projects: {
		id: string;
		key: string;
		customer: string;
		name: string;
		description: string;
		version: number;
	}[];
	bindings: {
		id: string;
		claim: string;
		value: string;
		role: string;
		scope: string;
		bootstrap: boolean;
	}[];
	/** Bindings returned by /me for the signed-in user. */
	me: { role: string; scope: string }[];
	items: FakeItem[];
	deps: { id: string; from: string; to: string; type: 'blocks' | 'relates' }[];
}

export interface FakeItem {
	id: string;
	key: string;
	project: string;
	kind: 'milestone' | 'epic' | 'ticket';
	title: string;
	description: string;
	state: string;
	type?: string;
	acceptance_criteria?: string[];
	policy?: { review_mode: string; merge_mode: string };
	epic?: string;
	milestone?: string;
	version: number;
}

const ROLES = [
	{
		role: 'org-admin',
		actions: [
			'customer.create',
			'customer.read',
			'customer.update',
			'project.create',
			'project.read',
			'role_binding.manage',
			'role_binding.read'
		]
	},
	{
		role: 'customer-admin',
		actions: [
			'customer.read',
			'customer.update',
			'project.create',
			'project.read',
			'role_binding.manage',
			'role_binding.read'
		]
	},
	{ role: 'engineer', actions: ['customer.read', 'project.read', 'tracker.read', 'tracker.write'] },
	{ role: 'approver', actions: ['customer.read', 'project.read', 'tracker.read'] },
	{ role: 'viewer', actions: ['customer.read', 'project.read', 'tracker.read'] }
];

const now = '2026-10-01T00:00:00Z';
let seq = 0;
const id = () => `id-${++seq}`;

function err(r: Route, status: number, error: string, message: string) {
	return r.fulfill({ status, json: { error, message } });
}

function ref(i: FakeItem) {
	return { key: i.key, kind: i.kind, title: i.title, state: i.state };
}

export async function fakeCore(page: Page, state: Partial<FakeCore> = {}): Promise<FakeCore> {
	const core: FakeCore = {
		customers: [],
		projects: [],
		bindings: [],
		me: [],
		items: [],
		deps: [],
		...state
	};
	const itemJSON = (i: FakeItem) => withTimes(i);
	const resolved = (key: string) =>
		['done', 'cancelled'].includes(core.items.find((i) => i.key === key)?.state ?? '');
	const withTimes = <T extends object>(o: T) => ({ ...o, created_at: now, updated_at: now });

	await page.route('**/api/v1/**', async (r) => {
		const url = new URL(r.request().url());
		const method = r.request().method();
		const path = url.pathname.replace('/api/v1', '');
		const body = r.request().postDataJSON?.() ?? {};
		let m: RegExpMatchArray | null;

		if (path === '/me') {
			return r.fulfill({
				json: {
					subject: 'user-alice',
					email: 'alice@ballet.test',
					name: 'Alice Admin',
					groups: [],
					bindings: core.me.map((b, i) => ({
						id: `me-${i}`,
						claim: 'groups',
						value: 'g',
						bootstrap: false,
						...b
					}))
				}
			});
		}
		if (path === '/roles') return r.fulfill({ json: { items: ROLES } });
		if (path === '/customers' && method === 'GET') {
			return r.fulfill({ json: { items: core.customers.map(withTimes) } });
		}
		if (path === '/customers' && method === 'POST') {
			if (core.customers.some((c) => c.key === body.key)) {
				return err(r, 409, 'already_exists', `create customer: already exists`);
			}
			const c = { id: id(), key: body.key, name: body.name, version: 1 };
			core.customers.push(c);
			return r.fulfill({ status: 201, json: withTimes(c) });
		}
		if ((m = path.match(/^\/customers\/([^/]+)$/))) {
			const c = core.customers.find((x) => x.key === m![1]);
			if (!c) return err(r, 404, 'not_found', 'customer not found');
			if (method === 'PATCH') {
				if (body.version !== c.version) return err(r, 409, 'conflict', 'stale version');
				c.name = body.name;
				c.version++;
			}
			return r.fulfill({ json: withTimes(c) });
		}
		if ((m = path.match(/^\/customers\/([^/]+)\/projects$/))) {
			if (method === 'POST') {
				const p = {
					id: id(),
					key: body.key,
					customer: m[1],
					name: body.name,
					description: body.description ?? '',
					version: 1
				};
				core.projects.push(p);
				return r.fulfill({ status: 201, json: withTimes(p) });
			}
			return r.fulfill({
				json: { items: core.projects.filter((p) => p.customer === m![1]).map(withTimes) }
			});
		}
		if ((m = path.match(/^\/projects\/([^/]+)$/))) {
			const p = core.projects.find((x) => x.key === m![1]);
			return p ? r.fulfill({ json: withTimes(p) }) : err(r, 404, 'not_found', 'project not found');
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/items$/))) {
			if (method === 'POST') {
				const n = core.items.filter((i) => i.project === m![1]).length + 1;
				const it: FakeItem = {
					id: id(),
					key: `${m[1]}-${n}`,
					project: m[1],
					kind: body.kind,
					title: body.title,
					description: body.description ?? '',
					state: body.kind === 'ticket' ? 'backlog' : 'open',
					version: 1
				};
				if (body.kind === 'ticket') {
					it.type = body.type ?? 'feature';
					it.acceptance_criteria = [];
					it.policy = { review_mode: 'agent', merge_mode: 'auto' };
				}
				core.items.push(it);
				return r.fulfill({ status: 201, json: itemJSON(it) });
			}
			return r.fulfill({
				json: { items: core.items.filter((i) => i.project === m![1]).map(itemJSON) }
			});
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/runnable$/))) {
			const run = core.items.filter(
				(i) =>
					i.project === m![1] &&
					i.kind === 'ticket' &&
					i.state === 'ready' &&
					core.deps.every((d) => d.to !== i.key || d.type !== 'blocks' || resolved(d.from))
			);
			return r.fulfill({ json: { items: run.map(itemJSON) } });
		}
		if ((m = path.match(/^\/items\/([^/]+)(\/[a-z]+)?$/))) {
			const it = core.items.find((i) => i.key === m![1]);
			if (!it) return err(r, 404, 'not_found', 'item not found');
			const sub = m[2] ?? '';
			if (sub === '' && method === 'PATCH') {
				if (body.version !== it.version) return err(r, 409, 'conflict', 'stale version');
				const changes = { ...body };
				delete changes.version;
				Object.assign(it, changes);
				if (changes.epic === '') delete it.epic;
				if (changes.milestone === '') delete it.milestone;
				it.version++;
			}
			if (sub === '/transition') {
				if (body.version !== it.version) return err(r, 409, 'conflict', 'stale version');
				it.state = body.state;
				it.version++;
			}
			if (sub === '/history') {
				return r.fulfill({
					json: {
						items: [
							{
								seq: 1,
								type: 'item.created',
								occurred_at: now,
								actor: { kind: 'human', subject: 'alice' }
							}
						]
					}
				});
			}
			if (sub === '/dependencies') {
				if (method === 'POST') {
					const other = core.items.find((i) => i.key === body.item);
					if (!other) return err(r, 400, 'invalid_argument', `${body.item} does not exist`);
					const d =
						body.type === 'blocked_by'
							? { id: id(), from: other.key, to: it.key, type: 'blocks' as const }
							: {
									id: id(),
									from: it.key,
									to: other.key,
									type: body.type === 'relates' ? ('relates' as const) : ('blocks' as const)
								};
					core.deps.push(d);
					return r.fulfill({ status: 201, json: { id: d.id, type: body.type, item: ref(other) } });
				}
				const views = core.deps
					.filter((d) => d.from === it.key || d.to === it.key)
					.map((d) => {
						const otherKey = d.from === it.key ? d.to : d.from;
						const type =
							d.type === 'relates' ? 'relates' : d.from === it.key ? 'blocks' : 'blocked_by';
						return { id: d.id, type, item: ref(core.items.find((i) => i.key === otherKey)!) };
					});
				return r.fulfill({ json: { items: views } });
			}
			return r.fulfill({ json: itemJSON(it) });
		}
		if ((m = path.match(/^\/dependencies\/(.+)$/)) && method === 'DELETE') {
			core.deps = core.deps.filter((d) => d.id !== m![1]);
			return r.fulfill({ status: 204 });
		}
		if (path === '/role-bindings' && method === 'GET')
			return r.fulfill({ json: { items: core.bindings } });
		if (path === '/role-bindings' && method === 'POST') {
			const b = { id: id(), bootstrap: false, ...body };
			core.bindings.push(b);
			return r.fulfill({ status: 201, json: { ...b, created_at: now } });
		}
		if ((m = path.match(/^\/role-bindings\/(.+)$/)) && method === 'DELETE') {
			core.bindings = core.bindings.filter((b) => b.id !== m![1]);
			return r.fulfill({ status: 204 });
		}
		return err(r, 404, 'not_found', `fake core: no route ${method} ${path}`);
	});
	return core;
}
