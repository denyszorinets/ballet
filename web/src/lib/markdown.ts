import DOMPurify from 'dompurify';
import { marked } from 'marked';

/**
 * Renders untrusted Markdown (knowledge written by humans and agents) to
 * sanitized HTML: scripts, event handlers and javascript: URLs are removed.
 */
export function renderMarkdown(source: string): string {
	const html = marked.parse(source, { async: false, gfm: true, breaks: false });
	return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } });
}
