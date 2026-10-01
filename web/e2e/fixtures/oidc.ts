import type { Page, Route } from '@playwright/test';

/** Fake OIDC provider for e2e tests (no signature: the SPA does not verify tokens). */
export const ISSUER = 'http://idp.test/realms/ballet';

function b64url(v: object): string {
	return Buffer.from(JSON.stringify(v)).toString('base64url');
}

function jwt(claims: object): string {
	return `${b64url({ alg: 'none', typ: 'JWT' })}.${b64url(claims)}.`;
}

export interface FakeUser {
	sub: string;
	name: string;
	email: string;
	groups: string[];
}

export const alice: FakeUser = {
	sub: 'user-alice',
	name: 'Alice Admin',
	email: 'alice@ballet.test',
	groups: ['ballet-admins']
};

/**
 * Routes /config.json and the fake issuer's endpoints. Logging in at the
 * fake authorize endpoint succeeds immediately as `user`.
 */
export async function fakeOIDC(page: Page, user: FakeUser = alice): Promise<void> {
	let nonce = '';
	await page.route('**/config.json', (r) =>
		r.fulfill({ json: { oidc: { issuer: ISSUER, client_id: 'ballet-web' } } })
	);
	await page.route(`${ISSUER}/.well-known/openid-configuration`, (r) =>
		r.fulfill({
			json: {
				issuer: ISSUER,
				authorization_endpoint: `${ISSUER}/auth`,
				token_endpoint: `${ISSUER}/token`,
				end_session_endpoint: `${ISSUER}/logout`,
				jwks_uri: `${ISSUER}/certs`,
				response_types_supported: ['code'],
				subject_types_supported: ['public'],
				id_token_signing_alg_values_supported: ['RS256']
			}
		})
	);
	await page.route(`${ISSUER}/auth?**`, (r: Route) => {
		const url = new URL(r.request().url());
		nonce = url.searchParams.get('nonce') ?? '';
		const back = new URL(url.searchParams.get('redirect_uri')!);
		back.searchParams.set('code', 'test-code');
		back.searchParams.set('state', url.searchParams.get('state')!);
		return r.fulfill({ status: 302, headers: { location: back.toString() } });
	});
	await page.route(`${ISSUER}/token`, (r) => {
		const now = Math.floor(Date.now() / 1000);
		const claims = { iss: ISSUER, sub: user.sub, iat: now, exp: now + 900, ...user };
		return r.fulfill({
			json: {
				access_token: jwt({ ...claims, aud: 'ballet' }),
				id_token: jwt({ ...claims, aud: 'ballet-web', nonce }),
				refresh_token: 'test-refresh',
				token_type: 'Bearer',
				expires_in: 900,
				scope: 'openid profile email'
			}
		});
	});
	await page.route(`${ISSUER}/logout?**`, (r) => {
		const url = new URL(r.request().url());
		const back = url.searchParams.get('post_logout_redirect_uri') ?? '/';
		return r.fulfill({ status: 302, headers: { location: back } });
	});
}
