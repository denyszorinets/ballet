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
	{ role: 'engineer', actions: ['customer.read', 'project.read'] },
	{ role: 'approver', actions: ['customer.read', 'project.read'] },
	{ role: 'viewer', actions: ['customer.read', 'project.read'] }
];

const now = '2026-10-01T00:00:00Z';
let seq = 0;
const id = () => `id-${++seq}`;

function err(r: Route, status: number, error: string, message: string) {
	return r.fulfill({ status, json: { error, message } });
}

export async function fakeCore(page: Page, state: Partial<FakeCore> = {}): Promise<FakeCore> {
	const core: FakeCore = { customers: [], projects: [], bindings: [], me: [], ...state };
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
