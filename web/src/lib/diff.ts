/** One line of a diff: kept (' '), removed ('-') or added ('+'). */
export interface DiffLine {
	op: ' ' | '-' | '+';
	text: string;
}

const lines = (s: string) => (s === '' ? [] : s.split('\n'));

/**
 * Line diff of a → b from their longest common subsequence. Quadratic in
 * the number of lines, which is fine for skill documents.
 */
export function diffLines(a: string, b: string): DiffLine[] {
	const x = lines(a);
	const y = lines(b);
	// lcs[i][j]: LCS length of x[i:] and y[j:].
	const lcs = Array.from({ length: x.length + 1 }, () => new Array<number>(y.length + 1).fill(0));
	for (let i = x.length - 1; i >= 0; i--) {
		for (let j = y.length - 1; j >= 0; j--) {
			lcs[i][j] = x[i] === y[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
		}
	}
	const out: DiffLine[] = [];
	let i = 0;
	let j = 0;
	while (i < x.length && j < y.length) {
		if (x[i] === y[j]) {
			out.push({ op: ' ', text: x[i] });
			i++;
			j++;
		} else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
			out.push({ op: '-', text: x[i++] });
		} else {
			out.push({ op: '+', text: y[j++] });
		}
	}
	while (i < x.length) out.push({ op: '-', text: x[i++] });
	while (j < y.length) out.push({ op: '+', text: y[j++] });
	return out;
}

export interface FileDiff {
	path: string;
	status: 'added' | 'removed' | 'changed';
	lines: DiffLine[];
}

/** Per-file diff of two path → content maps; unchanged files are omitted. */
export function diffFiles(a: Record<string, string>, b: Record<string, string>): FileDiff[] {
	const paths = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
	return paths.flatMap((path): FileDiff[] => {
		if (a[path] === b[path]) return [];
		const status = !(path in a) ? 'added' : !(path in b) ? 'removed' : 'changed';
		return [{ path, status, lines: diffLines(a[path] ?? '', b[path] ?? '') }];
	});
}
