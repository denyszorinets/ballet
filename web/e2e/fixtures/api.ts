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
	knowledge: FakeEntry[];
	skills: FakeSkill[];
	pins: { project: string; name: string; version: number; disabled: boolean }[];
	plannerSessions: FakePlannerSession[];
	/** Transcripts by session ID. */
	plannerMessages: Record<string, FakePlannerMessage[]>;
	changesets: FakeChangeset[];
	/** Execution settings by project key. */
	execution: Record<string, Record<string, unknown>>;
	/** Git tokens by project key (as the fake received them). */
	gitTokens: Record<string, string>;
}

export interface FakePlannerSession {
	id: string;
	project: string;
	title: string;
	created_by: string;
	running: boolean;
}

export interface FakePlannerMessage {
	seq: number;
	role: 'user' | 'assistant';
	content: {
		type: 'text' | 'tool_use' | 'tool_result';
		text?: string;
		tool_use_id?: string;
		name?: string;
		input?: Record<string, unknown>;
		is_error?: boolean;
	}[];
	author?: string;
}

export interface FakeChangeset {
	id: string;
	project: string;
	title: string;
	summary: string;
	status: 'proposed' | 'applied' | 'rejected';
	operations: {
		kind: 'create_item' | 'update_item' | 'add_dependency';
		ref?: string;
		create?: { kind: 'milestone' | 'epic' | 'ticket'; title: string; epic?: string };
		update?: { item: string; title?: string };
		dependency?: { from: string; to: string; type: 'blocks' | 'relates' };
	}[];
	approved: number[];
	results: { key?: string; dependency?: string }[];
}

export interface FakeSkill {
	id: string;
	scope: string;
	name: string;
	description: string;
	body: string;
	files: Record<string, string>;
	version: number;
	/** Published versions, oldest first. */
	versions: { description: string; body: string; files: Record<string, string> }[];
}

export interface FakeEntry {
	id: string;
	customer: string;
	kind: 'document' | 'decision' | 'note' | 'debt';
	title: string;
	body: string;
	projects: string[];
	items: string[];
	version: number;
	/** Earlier versions, oldest first; filled in by updates. */
	history?: { version: number; title: string; body: string }[];
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
			'project.update',
			'credential.manage',
			'run.manage',
			'customer.create',
			'customer.read',
			'project.read',
			'tracker.read',
			'knowledge.read',
			'skill.read',
			'role_binding.manage',
			'role_binding.read',
			'project.create',
			'customer.update',
			'tracker.write',
			'knowledge.write',
			'skill.write'
		]
	},
	{
		role: 'customer-admin',
		actions: [
			'project.update',
			'credential.manage',
			'run.manage',
			'customer.read',
			'project.read',
			'tracker.read',
			'knowledge.read',
			'skill.read',
			'role_binding.manage',
			'role_binding.read',
			'project.create',
			'customer.update',
			'tracker.write',
			'knowledge.write',
			'skill.write'
		]
	},
	{
		role: 'engineer',
		actions: [
			'customer.read',
			'project.read',
			'tracker.read',
			'knowledge.read',
			'skill.read',
			'tracker.write',
			'knowledge.write'
		]
	},
	{
		role: 'approver',
		actions: ['customer.read', 'project.read', 'tracker.read', 'knowledge.read', 'skill.read']
	},
	{
		role: 'viewer',
		actions: ['customer.read', 'project.read', 'tracker.read', 'knowledge.read', 'skill.read']
	}
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
		knowledge: [],
		skills: [],
		pins: [],
		plannerSessions: [],
		plannerMessages: {},
		changesets: [],
		execution: {},
		gitTokens: {},
		...state
	};
	const itemJSON = (i: FakeItem) => withTimes(i);
	const resolved = (key: string) =>
		['done', 'cancelled'].includes(core.items.find((i) => i.key === key)?.state ?? '');
	const withTimes = <T extends object>(o: T) => ({ ...o, created_at: now, updated_at: now });
	const skillJSON = (sk: FakeSkill) => ({
		id: sk.id,
		scope: sk.scope,
		name: sk.name,
		description: sk.description,
		body: sk.body,
		files: sk.files,
		latest_version: sk.versions.length,
		version: sk.version,
		created_at: now,
		updated_at: now
	});
	const versionJSON = (v: FakeSkill['versions'][number], n: number) => ({
		number: n,
		...v,
		published_by: 'user-alice',
		published_at: now
	});
	const changesetJSON = (c: FakeChangeset) => ({
		...c,
		proposed_by: { kind: 'service', subject: 'planner:s', acting_for: 'user-alice' },
		created_at: now,
		version: 1
	});
	const entryJSON = (e: FakeEntry) => ({
		id: e.id,
		kind: e.kind,
		title: e.title,
		body: e.body,
		projects: e.projects,
		items: e.items,
		version: e.version,
		created_by: 'user-alice',
		updated_by: 'user-alice',
		created_at: now,
		updated_at: now
	});

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
		if ((m = path.match(/^\/customers\/([^/]+)\/knowledge\/(entries|search)$/))) {
			const c = m[1];
			const q = url.searchParams;
			if (m[2] === 'entries' && method === 'POST') {
				const e: FakeEntry = {
					id: id(),
					customer: c,
					kind: body.kind,
					title: body.title,
					body: body.body ?? '',
					projects: body.projects ?? [],
					items: body.items ?? [],
					version: 1
				};
				core.knowledge.push(e);
				return r.fulfill({ status: 201, json: entryJSON(e) });
			}
			const words = (q.get('q') ?? '').toLowerCase().split(/\s+/).filter(Boolean);
			const found = core.knowledge.filter(
				(e) =>
					e.customer === c &&
					(!q.get('kind') || e.kind === q.get('kind')) &&
					(!q.get('project') || e.projects.includes(q.get('project')!)) &&
					(!q.get('item') || e.items.includes(q.get('item')!)) &&
					words.every((w) => `${e.title} ${e.body}`.toLowerCase().includes(w))
			);
			const items =
				m[2] === 'search'
					? found.map((e) => ({ entry: entryJSON(e), score: 1 }))
					: found.map(entryJSON);
			return r.fulfill({ json: { items } });
		}
		if ((m = path.match(/^\/customers\/([^/]+)\/knowledge\/entries\/([^/]+)(\/versions)?$/))) {
			const e = core.knowledge.find((x) => x.customer === m![1] && x.id === m![2]);
			if (!e) return err(r, 404, 'not_found', 'entry not found');
			if (m[3]) {
				const all = [...(e.history ?? []), { version: e.version, title: e.title, body: e.body }];
				return r.fulfill({
					json: { items: all.map((v) => ({ ...v, author: 'user-alice', created_at: now })) }
				});
			}
			if (method === 'PATCH') {
				if (body.version !== e.version) return err(r, 409, 'conflict', 'stale version');
				e.history = [...(e.history ?? []), { version: e.version, title: e.title, body: e.body }];
				const changes = { ...body };
				delete changes.version;
				Object.assign(e, changes);
				e.version++;
			}
			return r.fulfill({ json: entryJSON(e) });
		}
		if (path === '/skills' && method === 'GET') {
			const items = core.skills.filter((x) => x.scope === url.searchParams.get('scope'));
			return r.fulfill({ json: { items: items.map(skillJSON) } });
		}
		if (path === '/skills' && method === 'POST') {
			if (core.skills.some((x) => x.scope === body.scope && x.name === body.name)) {
				return err(r, 409, 'already_exists', 'skill already exists');
			}
			const sk: FakeSkill = {
				id: id(),
				scope: body.scope,
				name: body.name,
				description: body.description,
				body: body.body ?? '',
				files: body.files ?? {},
				version: 1,
				versions: []
			};
			core.skills.push(sk);
			return r.fulfill({ status: 201, json: skillJSON(sk) });
		}
		if ((m = path.match(/^\/skills\/([^/]+)(\/[a-z]+)?$/))) {
			const sk = core.skills.find((x) => x.id === m![1]);
			if (!sk) return err(r, 404, 'not_found', 'skill not found');
			const sub = m[2] ?? '';
			if (sub === '/versions') {
				return r.fulfill({ json: { items: sk.versions.map((v, i) => versionJSON(v, i + 1)) } });
			}
			if (sub === '/publish') {
				if (body.version !== sk.version) return err(r, 409, 'conflict', 'stale version');
				sk.versions.push({ description: sk.description, body: sk.body, files: { ...sk.files } });
				return r.fulfill({
					status: 201,
					json: versionJSON(sk.versions.at(-1)!, sk.versions.length)
				});
			}
			if (method === 'PATCH') {
				if (body.version !== sk.version) return err(r, 409, 'conflict', 'stale version');
				sk.description = body.description ?? sk.description;
				sk.body = body.body ?? sk.body;
				sk.files = body.files ?? sk.files;
				sk.version++;
			}
			return r.fulfill({ json: skillJSON(sk) });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/skill-pins$/))) {
			const items = core.pins.filter((p) => p.project === m![1]);
			return r.fulfill({
				json: { items: items.map(({ name, version, disabled }) => ({ name, version, disabled })) }
			});
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/skills\/([^/]+)\/pin$/))) {
			const [, project, name] = m;
			core.pins = core.pins.filter((p) => p.project !== project || p.name !== name);
			if (method === 'PUT') {
				core.pins.push({ project, name, version: body.version ?? 0, disabled: !!body.disabled });
			}
			return r.fulfill({ status: 204 });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/skills$/))) {
			const project = core.projects.find((p) => p.key === m![1]);
			const chain = ['organization', `customer:${project?.customer}`, `project:${m[1]}`];
			const best: Record<string, FakeSkill> = {};
			for (const sk of core.skills) {
				const rank = chain.indexOf(sk.scope);
				if (rank >= 0 && (!best[sk.name] || rank > chain.indexOf(best[sk.name].scope))) {
					best[sk.name] = sk;
				}
			}
			const items = Object.values(best).flatMap((sk) => {
				const pin = core.pins.find((p) => p.project === m![1] && p.name === sk.name);
				if (pin?.disabled || sk.versions.length === 0) return [];
				const pinned = !!pin && pin.version > 0;
				const version = pinned ? pin!.version : sk.versions.length;
				return [
					{
						name: sk.name,
						skill_id: sk.id,
						scope: sk.scope,
						version,
						latest_version: sk.versions.length,
						pinned
					}
				];
			});
			return r.fulfill({ json: { items } });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/planner\/sessions$/))) {
			if (method === 'POST') {
				const ps: FakePlannerSession = {
					id: id(),
					project: m[1],
					title: body.title,
					created_by: 'user-alice',
					running: false
				};
				core.plannerSessions.push(ps);
				core.plannerMessages[ps.id] = [];
				return r.fulfill({ status: 201, json: withTimes(ps) });
			}
			const items = core.plannerSessions.filter((x) => x.project === m![1]).map(withTimes);
			return r.fulfill({ json: { items } });
		}
		if ((m = path.match(/^\/planner\/sessions\/([^/]+)$/))) {
			const ps = core.plannerSessions.find((x) => x.id === m![1]);
			if (!ps) return err(r, 404, 'not_found', 'planner session not found');
			const usage = {
				input_tokens: 0,
				output_tokens: 0,
				cache_read_tokens: 0,
				cache_write_tokens: 0
			};
			const messages = (core.plannerMessages[ps.id] ?? []).map((msg) => ({
				...msg,
				usage,
				created_at: now
			}));
			return r.fulfill({ json: { ...withTimes(ps), messages } });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/changesets$/))) {
			const st = url.searchParams.get('status');
			const items = core.changesets
				.filter((c) => c.project === m![1] && (!st || c.status === st))
				.reverse()
				.map(changesetJSON);
			return r.fulfill({ json: { items } });
		}
		if ((m = path.match(/^\/changesets\/([^/]+)(\/apply|\/reject)?$/))) {
			const cs = core.changesets.find((c) => c.id === m![1]);
			if (!cs) return err(r, 404, 'not_found', 'changeset not found');
			if (m[2] && cs.status !== 'proposed')
				return err(r, 409, 'conflict', `changeset is already ${cs.status}`);
			if (m[2] === '/reject') cs.status = 'rejected';
			if (m[2] === '/apply') {
				const approved: number[] = body.operations;
				const keys: Record<string, string> = {};
				const resolveRef = (ref?: string) => (ref?.startsWith('$') ? keys[ref.slice(1)] : ref);
				cs.results = cs.operations.map(() => ({}));
				for (const i of approved) {
					const op = cs.operations[i];
					if (op.create) {
						const n = core.items.filter((it) => it.project === cs.project).length + 1;
						const it: FakeItem = {
							id: id(),
							key: `${cs.project}-${n}`,
							project: cs.project,
							kind: op.create.kind,
							title: op.create.title,
							description: '',
							state: op.create.kind === 'ticket' ? 'backlog' : 'open',
							version: 1
						};
						const epic = resolveRef(op.create.epic);
						if (epic) it.epic = epic;
						core.items.push(it);
						keys[op.ref!] = it.key;
						cs.results[i] = { key: it.key };
					} else if (op.update) {
						const it = core.items.find((x) => x.key === op.update!.item);
						if (it && op.update.title) it.title = op.update.title;
						cs.results[i] = { key: op.update.item };
					} else if (op.dependency) {
						const d = {
							id: id(),
							from: resolveRef(op.dependency.from)!,
							to: resolveRef(op.dependency.to)!,
							type: op.dependency.type
						};
						core.deps.push(d);
						cs.results[i] = { dependency: d.id };
					}
				}
				cs.approved = approved;
				cs.status = 'applied';
			}
			return r.fulfill({ json: changesetJSON(cs) });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/execution$/))) {
			const cur = core.execution[m[1]] ?? {
				project: m[1],
				repo_url: '',
				default_branch: '',
				image: '',
				setup: [],
				env: {},
				branch_template: 'ballet/{ticket}-{slug}',
				git_name: '',
				git_email: '',
				version: 0
			};
			if (method === 'PUT') {
				if (body.version !== cur.version) return err(r, 409, 'conflict', 'stale version');
				if (body.repo_url && !/^(https?|ssh|file):\/\//.test(body.repo_url)) {
					return err(
						r,
						400,
						'invalid_argument',
						'repo_url must be an https, ssh, git@host:path or file URL'
					);
				}
				const next = { ...cur, ...body, project: m[1], version: cur.version + 1 };
				core.execution[m[1]] = next;
				return r.fulfill({ json: next });
			}
			return r.fulfill({ json: cur });
		}
		if ((m = path.match(/^\/projects\/([^/]+)\/credentials\/git$/)) && method === 'PUT') {
			core.gitTokens[m[1]] = body.api_key;
			return r.fulfill({
				json: {
					provider: 'git',
					project: m[1],
					base_url: '',
					fingerprint: 'abcd…' + body.api_key.slice(-4),
					updated_at: now
				}
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
