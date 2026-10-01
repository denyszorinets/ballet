export type Theme = 'light' | 'dark';

const KEY = 'ballet.theme';

/** The stored theme preference, or undefined to follow the system. */
export function storedTheme(): Theme | undefined {
	try {
		const t = localStorage.getItem(KEY);
		return t === 'light' || t === 'dark' ? t : undefined;
	} catch {
		return undefined;
	}
}

/** Applies and remembers a theme. */
export function setTheme(theme: Theme): void {
	document.documentElement.dataset.theme = theme;
	try {
		localStorage.setItem(KEY, theme);
	} catch {
		// Storage unavailable (private mode): the choice lasts for this page.
	}
}

/** The theme currently in effect. */
export function currentTheme(): Theme {
	const explicit = document.documentElement.dataset.theme;
	if (explicit === 'light' || explicit === 'dark') return explicit;
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}
