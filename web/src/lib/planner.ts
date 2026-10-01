import type { Schemas } from '$lib/api/client';
import type { RealtimeClient } from '$lib/realtime/client';

export type PlannerMessage = Schemas['PlannerMessage'];
export type PlannerBlock = Schemas['PlannerBlock'];

/** A tool call with its result, once known. */
export interface ToolCall {
	id: string;
	name: string;
	input: unknown;
	result?: string;
	isError?: boolean;
	/** The changeset a propose_changeset call created. */
	changeset?: string;
}

/** What the chat shows: human text, assistant text and tool calls, in order. */
export type ChatEntry =
	| { kind: 'human'; seq: number; text: string; author?: string }
	| { kind: 'assistant'; seq: number; text: string }
	| { kind: 'tool'; seq: number; call: ToolCall };

/** Turns a transcript into chat entries, attaching tool results to their calls. */
export function chatEntries(messages: PlannerMessage[]): ChatEntry[] {
	const out: ChatEntry[] = [];
	const calls = new Map<string, ToolCall>();
	for (const m of messages) {
		for (const b of m.content) {
			if (b.type === 'text' && b.text) {
				out.push(
					m.role === 'user'
						? { kind: 'human', seq: m.seq, text: b.text, author: m.author }
						: { kind: 'assistant', seq: m.seq, text: b.text }
				);
			} else if (b.type === 'tool_use' && b.tool_use_id) {
				const call: ToolCall = { id: b.tool_use_id, name: b.name ?? '', input: b.input ?? {} };
				calls.set(call.id, call);
				out.push({ kind: 'tool', seq: m.seq, call });
			} else if (b.type === 'tool_result' && b.tool_use_id) {
				const call = calls.get(b.tool_use_id);
				if (!call) continue;
				call.result = b.text ?? '';
				call.isError = b.is_error ?? false;
				if (call.name === 'propose_changeset' && !call.isError) {
					try {
						call.changeset = (JSON.parse(call.result) as { changeset?: string }).changeset;
					} catch {
						// not JSON: no card
					}
				}
			}
		}
	}
	return out;
}

/** Live output of a planner turn (planner.output). */
export interface PlannerOutput {
	subscription: string;
	session: string;
	type: 'text' | 'tool_call' | 'tool_result' | 'message' | 'done' | 'error';
	text?: string;
	tool?: string;
	tool_use_id?: string;
	input?: unknown;
	is_error?: boolean;
	seq?: number;
}

export interface WatchHandlers {
	/** Live output of the session. */
	onOutput: (o: PlannerOutput) => void;
	/** (Re)watching started: reload the transcript; running tells whether a turn is in progress. */
	onReset: (running: boolean) => void;
}

let nextWatch = 0;

/**
 * Watches a planner session: watches again after every reconnect or when
 * the server drops the watch, calling onReset each time. Returns a stop
 * function.
 */
export function watchSession(rt: RealtimeClient, session: string, h: WatchHandlers): () => void {
	let id = '';
	let stopped = false;
	const start = async () => {
		id = `pw${++nextWatch}`;
		try {
			const r = await rt.call<{ running: boolean }>('planner.watch', {
				subscription: id,
				session
			});
			if (!stopped) h.onReset(r.running);
		} catch {
			// the connection dropped: the next "open" status watches again
		}
	};
	const offOutput = rt.on('planner.output', (p) => {
		const o = p as PlannerOutput;
		if (o.subscription === id) h.onOutput(o);
	});
	const offClosed = rt.on('stream.closed', (p) => {
		if ((p as { subscription: string }).subscription === id) void start();
	});
	const offStatus = rt.onStatus((s) => {
		if (s === 'open') void start();
	});
	return () => {
		stopped = true;
		offOutput();
		offClosed();
		offStatus();
		if (rt.status === 'open') rt.call('stream.unsubscribe', { subscription: id }).catch(() => {});
	};
}
