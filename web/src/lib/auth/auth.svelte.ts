import { UserManager, WebStorageStateStore, type User } from 'oidc-client-ts';
import type { AppConfig } from '$lib/config';

/** Where to return after login, kept in the OIDC state. */
interface LoginState {
	returnTo: string;
}

/**
 * OIDC login for the SPA: authorization code flow with PKCE against the
 * issuer from /config.json; tokens are kept in sessionStorage and renewed
 * silently with the refresh token.
 */
export class Auth {
	user = $state<User | null>(null);
	private manager: UserManager;

	constructor(config: AppConfig, origin: string) {
		this.manager = new UserManager({
			authority: config.oidc.issuer,
			client_id: config.oidc.client_id,
			redirect_uri: `${origin}/auth/callback`,
			post_logout_redirect_uri: `${origin}/`,
			response_type: 'code',
			scope: 'openid profile email',
			automaticSilentRenew: true,
			userStore: new WebStorageStateStore({ store: window.sessionStorage })
		});
		this.manager.events.addUserLoaded((u) => {
			this.user = u;
		});
		this.manager.events.addUserUnloaded(() => {
			this.user = null;
		});
		this.manager.events.addSilentRenewError(() => {
			this.user = null;
		});
	}

	/** Loads a stored session, if any. */
	async init(): Promise<void> {
		const u = await this.manager.getUser();
		this.user = u && !u.expired ? u : null;
	}

	get authenticated(): boolean {
		return this.user !== null;
	}

	get displayName(): string {
		const p = this.user?.profile;
		return p?.name ?? p?.preferred_username ?? p?.email ?? p?.sub ?? '';
	}

	/** Redirects to the identity provider. */
	login(returnTo: string): Promise<void> {
		return this.manager.signinRedirect({ state: { returnTo } satisfies LoginState });
	}

	/** Completes the login on /auth/callback; returns where to go next. */
	async completeLogin(url: string): Promise<string> {
		const u = await this.manager.signinRedirectCallback(url);
		this.user = u;
		const returnTo = (u.state as LoginState | undefined)?.returnTo ?? '/';
		return returnTo.startsWith('/') && !returnTo.startsWith('//') ? returnTo : '/';
	}

	/** Ends the session at the identity provider. */
	logout(): Promise<void> {
		return this.manager.signoutRedirect();
	}

	/** Current access token, or undefined when logged out. */
	async getToken(): Promise<string | undefined> {
		const u = await this.manager.getUser();
		return u && !u.expired ? u.access_token : undefined;
	}
}
