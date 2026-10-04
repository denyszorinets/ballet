import { UserManager, WebStorageStateStore, type User } from 'oidc-client-ts';
import type { AppConfig } from '$lib/config';

/** Where to return after login, kept in the OIDC state. */
interface LoginState {
	returnTo: string;
}

/** The token sent when Core runs without authentication (it ignores it). */
const localToken = 'local';

/**
 * OIDC login for the SPA: authorization code flow with PKCE against the
 * issuer from /config.json; tokens are kept in sessionStorage and renewed
 * silently with the refresh token. Without an issuer, Core runs without
 * authentication: the SPA is always signed in as the local user.
 */
export class Auth {
	user = $state<User | null>(null);
	/** Core runs without authentication (no issuer configured). */
	readonly local: boolean;
	private manager: UserManager;

	constructor(config: AppConfig, origin: string) {
		this.local = !config.oidc.issuer;
		this.manager = new UserManager({
			authority: config.oidc.issuer || origin,
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
		if (this.local) return;
		const u = await this.manager.getUser();
		this.user = u && !u.expired ? u : null;
	}

	get authenticated(): boolean {
		return this.local || this.user !== null;
	}

	/** The signed-in user's subject (OIDC "sub"). */
	get subject(): string {
		if (this.local) return 'local';
		return this.user?.profile?.sub ?? '';
	}

	get displayName(): string {
		if (this.local) return 'Local user';
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
		if (this.local) return localToken;
		const u = await this.manager.getUser();
		return u && !u.expired ? u.access_token : undefined;
	}
}
