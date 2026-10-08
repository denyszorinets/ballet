import type { Schemas } from '$lib/api/client';

type RunLog = Schemas['RunLog'];

/** One entry of a session's transcript. */
export type Entry =
	| { kind: 'text'; text: string }
	| { kind: 'tool_use'; tool: string; input: string }
	| { kind: 'tool_result'; text: string; error: boolean }
	| { kind: 'result'; text: string; error: boolean }
	| { kind: 'output'; stream: string; text: string };

const maxInput = 200;

/** Shortens a tool's JSON input to "key: value, …". */
function summarize(input: string): string {
	let s = input;
	try {
		const v = JSON.parse(input) as unknown;
		if (v && typeof v === 'object' && !Array.isArray(v))
			s = Object.entries(v as Record<string, unknown>)
				.map(([k, x]) => `${k}: ${typeof x === 'string' ? x : JSON.stringify(x)}`)
				.join(', ');
	} catch {
		// not JSON: shown as is
	}
	s = s.replace(/\s+/g, ' ');
	return s.length > maxInput ? s.slice(0, maxInput) + '…' : s;
}

/**
 * Builds a run's transcript from its output: session events (stream
 * "event", one JSON object per line) become entries; other output is
 * merged per stream.
 */
export function transcript(logs: RunLog[]): Entry[] {
	const out: Entry[] = [];
	for (const l of logs) {
		if (l.stream !== 'event') {
			const last = out.at(-1);
			if (last?.kind === 'output' && last.stream === l.stream) last.text += l.text;
			else out.push({ kind: 'output', stream: l.stream, text: l.text });
			continue;
		}
		for (const line of l.text.split('\n')) {
			if (!line.trim()) continue;
			let e: { kind?: string; text?: string; tool?: string; input?: string; error?: boolean };
			try {
				e = JSON.parse(line);
			} catch {
				continue;
			}
			switch (e.kind) {
				case 'text':
					out.push({ kind: 'text', text: e.text ?? '' });
					break;
				case 'tool_use':
					out.push({ kind: 'tool_use', tool: e.tool ?? '', input: summarize(e.input ?? '') });
					break;
				case 'tool_result':
				case 'result':
					out.push({ kind: e.kind, text: e.text ?? '', error: !!e.error });
					break;
			}
		}
	}
	return out;
}
