import { describe, expect, it } from 'vitest';
import { parseKeys } from './knowledge';

describe('parseKeys', () => {
	it('splits on commas and whitespace and drops blanks and duplicates', () => {
		expect(parseKeys(' WEB-1, WEB-2\nWEB-1 ,, ')).toEqual(['WEB-1', 'WEB-2']);
		expect(parseKeys('')).toEqual([]);
	});
});
