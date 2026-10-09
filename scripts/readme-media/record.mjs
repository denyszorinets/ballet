// Records the README's animations from a running Ballet with the fake
// model (BALLET_FAKE_LLM=1 make run): sets up a demo project over the REST
// API, drives the web UI with Playwright and saves each animation's frames
// with their durations. gif.py turns them into GIFs. Run it through
// `make readme-media`, which starts and stops Ballet.
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '../../web/node_modules/playwright/index.mjs';

const base = process.env.BALLET_URL ?? 'http://localhost:8080';
const out = process.argv[2] ?? '.run/readme-frames';
const api = async (method, path, body) => {
	const r = await fetch(base + path, {
		method,
		headers: { 'content-type': 'application/json' },
		body: body && JSON.stringify(body)
	});
	if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
	return r.status === 204 ? null : r.json();
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const git = (cwd, ...args) => execFileSync('git', args, { cwd, stdio: 'pipe' }).toString();

// A demo repository; a bare clone stands in for GitHub.
const demo = mkdtempSync(join(tmpdir(), 'ballet-readme-'));
const work = join(demo, 'greeter');
mkdirSync(work);
git(work, 'init', '-q', '-b', 'main');
writeFileSync(join(work, 'README.md'), '# Greeter\n');
git(work, 'add', '.');
git(work, '-c', 'user.name=Demo', '-c', 'user.email=demo@example.com', 'commit', '-qm', 'init');
git(demo, 'clone', '-q', '--bare', work, 'greeter.git');
const repo = 'file://' + join(demo, 'greeter.git');

await api('POST', '/api/v1/customers', { key: 'acme', name: 'Acme Corporation' });
await api('PUT', '/api/v1/customers/acme/credentials/anthropic', { api_key: 'sk-fake' });
await api('POST', '/api/v1/customers/acme/projects', { key: 'GREET', name: 'Greeter', description: 'A tiny demo project.' });
const x = await api('GET', '/api/v1/projects/GREET/execution');
await api('PUT', '/api/v1/projects/GREET/execution', {
	version: x.version, repo_url: repo, default_branch: 'main', forge: 'git'
});
await api('PUT', '/api/v1/projects/GREET/credentials/git', { api_key: 'demo' });

// The fake model performs the "/tool <name> <json>" line of a ticket.
const bash = (cmd) => '/tool Bash ' + JSON.stringify({ command: cmd });
const commit = (file, text, msg) =>
	bash(`printf '${text}' > ${file} && git add -A && (git commit -qm '${msg}' || true) && git push -q origin HEAD`);
const ticket = (ref, title, type, description, criteria) => ({
	kind: 'create_item', ref, create: { kind: 'ticket', type, title, description, acceptance_criteria: criteria }
});
await api('POST', '/api/v1/projects/GREET/changesets', {
	title: 'Greeting command',
	summary: 'A `hello.sh` that greets someone, and a French greeting.',
	operations: [
		ticket('hello', 'Add hello.sh', 'feature', 'Print `Hello, <name>!`.\n\n' + commit('hello.sh', 'echo Hello, ${1:-world}!\\n', 'Add hello.sh'),
			['`./hello.sh Ada` prints `Hello, Ada!`']),
		ticket('french', 'Greet in French with --lang fr', 'feature', 'Add `--lang fr`.\n\n/tool mcp__tracker__raise_question ' +
			JSON.stringify({ question: 'Should `--lang` also accept full names like `french`?', blocking: true }),
			['`./hello.sh --lang fr Ada` prints `Bonjour, Ada !`']),
		ticket('lint', 'Add a shellcheck CI job', 'tech_debt', 'Lint shell scripts in CI.', ['CI fails on shellcheck warnings'])
	]
});

const browser = await chromium.launch({ channel: 'chrome' });
const page = await browser.newPage({ viewport: { width: 1200, height: 720 }, colorScheme: 'light' });

// A recording: frames with how long each is shown. Identical consecutive
// frames are merged, so waiting costs no frames.
function recording(name) {
	const dir = join(out, name);
	rmSync(dir, { recursive: true, force: true });
	mkdirSync(dir, { recursive: true });
	const frames = [];
	let last = null;
	return {
		async frame(ms = 120) {
			const png = await page.screenshot();
			if (last && png.equals(last)) {
				frames.at(-1).ms += ms;
				return;
			}
			last = png;
			const file = `${String(frames.length).padStart(3, '0')}.png`;
			writeFileSync(join(dir, file), png);
			frames.push({ file, ms });
		},
		// Frames every step ms until done() holds or timeout ms pass; each
		// distinct frame shows at least hold ms and at most cap ms.
		async until(done, { step = 300, timeout = 60000, hold = 1000, cap = 1400 } = {}) {
			const end = Date.now() + timeout;
			while (Date.now() < end) {
				const n = frames.length;
				await this.frame(step);
				if (frames.length > n) frames.at(-1).ms = hold;
				else frames.at(-1).ms = Math.min(frames.at(-1).ms, cap);
				if (await done()) return;
				await sleep(step);
			}
			throw new Error(`${name}: timed out`);
		},
		async type(locator, text) {
			await locator.click();
			for (let i = 0; i < text.length; i += 3) {
				await locator.fill(text.slice(0, i + 3));
				await this.frame(60);
			}
		},
		save() {
			writeFileSync(join(dir, 'frames.json'), JSON.stringify(frames, null, 2));
		}
	};
}
const state = async (key) => (await api('GET', `/api/v1/items/${key}`)).state;
const move = async (key, to) => {
	const it = await api('GET', `/api/v1/items/${key}`);
	await api('POST', `/api/v1/items/${key}/transition`, { state: to, version: it.version });
};

// 1. Approve the plan, start a ticket and follow it on the board.
{
	const r = recording('plan');
	await page.goto(base + '/projects/GREET/changesets');
	await page.getByRole('button', { name: 'Approve all' }).waitFor();
	await r.frame(1800);
	await page.getByRole('button', { name: 'Approve all' }).hover();
	await r.frame(500);
	await page.getByRole('button', { name: 'Approve all' }).click();
	await page.getByRole('link', { name: 'GREET-1' }).waitFor();
	await r.frame(1500);
	await page.goto(base + '/projects/GREET');
	await page.getByRole('link', { name: /GREET-1/ }).waitFor();
	await r.frame(1500);
	await move('GREET-1', 'ready');
	await r.until(async () => (await page.getByText('waiting for merge').count()) > 0 ||
		(await page.getByRole('listitem', { name: 'In progress' }).getByText('integrate').count()) > 0);
	await r.frame(1500);
	// A reviewer merges the branch; Ballet notices.
	git(work, 'pull', '-q', join(demo, 'greeter.git'), 'ballet/GREET-1-add-hello-sh');
	git(work, 'push', '-q', join(demo, 'greeter.git'), 'HEAD:main');
	await r.until(async () => (await state('GREET-1')) === 'done', { timeout: 120000, cap: 900 });
	await sleep(1500);
	await r.frame(2500);
	r.save();
}

// 2. A question answered while the session waits.
{
	const r = recording('question');
	await move('GREET-2', 'ready');
	await page.goto(base + '/inbox');
	await page.getByRole('textbox', { name: 'Your answer' }).waitFor({ timeout: 90000 });
	await r.frame(2000);
	await r.type(page.getByRole('textbox', { name: 'Your answer' }), 'Yes: accept `fr` and `french`, case-insensitive.');
	await r.frame(800);
	await page.getByRole('button', { name: 'Answer and resume' }).click();
	await sleep(1500);
	await r.frame(1200);
	await page.goto(base + '/items/GREET-2');
	await page.getByRole('button', { name: 'Show session' }).first().click();
	const answer = page.getByText('Answer to your question');
	await answer.waitFor({ timeout: 30000 });
	await answer.scrollIntoViewIfNeeded();
	await r.frame(3500);
	r.save();
	await move('GREET-2', 'cancelled');
}

// Stills: a session's transcript and the digest.
await page.goto(base + '/items/GREET-1');
await page.getByRole('button', { name: 'Show session' }).first().click();
const session = page.getByRole('listitem', { name: 'Session implement' });
await session.getByRole('region', { name: 'Session transcript' }).waitFor();
await sleep(600);
await session.screenshot({ path: join(out, 'session.png') });
await page.goto(base + '/projects/GREET/digest');
await page.getByRole('list', { name: 'Summary' }).waitFor();
await sleep(800);
await page.screenshot({ path: join(out, 'digest.png') });

await browser.close();
rmSync(demo, { recursive: true, force: true });
console.log(`frames in ${out}`);
