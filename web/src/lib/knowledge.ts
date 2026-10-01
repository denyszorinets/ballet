import type { Schemas } from '$lib/api/client';

export type KnowledgeKind = Schemas['KnowledgeKind'];
export type KnowledgeEntry = Schemas['KnowledgeEntry'];

export const KINDS: KnowledgeKind[] = ['document', 'decision', 'note', 'debt'];

export const kindLabel: Record<KnowledgeKind, string> = {
	document: 'Document',
	decision: 'Decision',
	note: 'Note',
	debt: 'Debt'
};

/** Splits a comma- or whitespace-separated list of keys, dropping blanks and duplicates. */
export function parseKeys(text: string): string[] {
	return [...new Set(text.split(/[\s,]+/).filter(Boolean))];
}
