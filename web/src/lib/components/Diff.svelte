<script lang="ts">
	import type { DiffLine } from '$lib/diff';

	let { lines, label }: { lines: DiffLine[]; label: string } = $props();
</script>

<pre class="diff" aria-label={label}>{#each lines as l, i (i)}<span
			class:add={l.op === '+'}
			class:del={l.op === '-'}
			>{l.op} {l.text}
</span>{/each}</pre>

<style>
	.diff {
		overflow-x: auto;
		padding: 0.5rem;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		font-family: ui-monospace, monospace;
		font-size: 0.85rem;
	}
	.add {
		color: var(--ok);
	}
	.del {
		color: var(--danger);
	}
</style>
