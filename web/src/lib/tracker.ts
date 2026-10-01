import type { Schemas } from '$lib/api/client';

export type Item = Schemas['Item'];
export type ItemState = Schemas['ItemState'];
export type ItemKind = Schemas['ItemKind'];

export const stateLabel: Record<ItemState, string> = {
	open: 'Open',
	backlog: 'Backlog',
	ready: 'Ready',
	in_progress: 'In progress',
	waiting_for_answer: 'Waiting for answer',
	paused: 'Paused',
	done: 'Done',
	cancelled: 'Cancelled'
};

/** Board columns for tickets, in order. */
export const boardColumns: ItemState[] = [
	'backlog',
	'ready',
	'in_progress',
	'waiting_for_answer',
	'paused',
	'done'
];

// Mirrors Core's human transition table (core/internal/domain/tracker);
// Core validates every transition regardless.
const ticketTransitions: Partial<Record<ItemState, ItemState[]>> = {
	backlog: ['ready', 'cancelled', 'done'],
	ready: ['backlog', 'paused', 'cancelled', 'done'],
	in_progress: ['paused', 'cancelled'],
	waiting_for_answer: ['paused', 'cancelled'],
	paused: ['ready', 'backlog', 'cancelled', 'done'],
	done: ['backlog'],
	cancelled: ['backlog']
};

const containerTransitions: Partial<Record<ItemState, ItemState[]>> = {
	open: ['done', 'cancelled'],
	done: ['open'],
	cancelled: ['open']
};

/** States a human may move an item to. */
export function nextStates(kind: ItemKind, state: ItemState): ItemState[] {
	const table = kind === 'ticket' ? ticketTransitions : containerTransitions;
	return table[state] ?? [];
}

/** Inserts or replaces an item (by key), keeping key-number order. */
export function upsert(items: Item[], item: Item): Item[] {
	const rest = items.filter((i) => i.key !== item.key);
	return [...rest, item].sort((a, b) => keyNumber(a.key) - keyNumber(b.key));
}

function keyNumber(key: string): number {
	return Number(key.slice(key.lastIndexOf('-') + 1));
}
