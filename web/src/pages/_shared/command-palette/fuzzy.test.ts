import { describe, it, expect } from 'vitest';
import { fuzzyMatch } from './fuzzy';

describe('fuzzyMatch', () => {
    it('empty query matches everything with score 0 and no ranges', () => {
        const result = fuzzyMatch('', 'hello world');
        expect(result).not.toBeNull();
        expect(result!.score).toBe(0);
        expect(result!.ranges).toEqual([]);
    });

    it('empty query matches empty text with score 0', () => {
        const result = fuzzyMatch('', '');
        expect(result).not.toBeNull();
        expect(result!.score).toBe(0);
        expect(result!.ranges).toEqual([]);
    });

    it('non-subsequence returns null', () => {
        expect(fuzzyMatch('xyz', 'hello')).toBeNull();
        expect(fuzzyMatch('abc', 'bca')).toBeNull();
    });

    it('prefix match outscores a scattered subsequence', () => {
        const prefix = fuzzyMatch('flu', 'flush rib fib');
        const scattered = fuzzyMatch('flu', 'full of luck');
        expect(prefix).not.toBeNull();
        expect(scattered).not.toBeNull();
        expect(prefix!.score).toBeGreaterThan(scattered!.score);
    });

    it('contiguous match outscores a non-contiguous match', () => {
        const contiguous = fuzzyMatch('abc', 'abcdef');
        const scattered = fuzzyMatch('abc', 'xaxbxc');
        expect(contiguous).not.toBeNull();
        expect(scattered).not.toBeNull();
        expect(contiguous!.score).toBeGreaterThan(scattered!.score);
    });

    it('produces correct ranges for a known input — contiguous prefix', () => {
        const result = fuzzyMatch('flu', 'flush rib fib');
        expect(result).not.toBeNull();
        expect(result!.ranges).toEqual([[0, 3]]);
    });

    it('produces correct ranges for a scattered match', () => {
        const result = fuzzyMatch('hlo', 'hello');
        expect(result).not.toBeNull();
        expect(result!.ranges).toEqual([[0, 1], [2, 3], [4, 5]]);
    });

    it('produces correct ranges for a two-run match', () => {
        const result = fuzzyMatch('ab', 'a-bc');
        expect(result).not.toBeNull();
        expect(result!.ranges).toEqual([[0, 1], [2, 3]]);
    });

    it('is case-insensitive', () => {
        expect(fuzzyMatch('FLUSH', 'flush rib fib')).not.toBeNull();
        expect(fuzzyMatch('flush', 'FLUSH RIB FIB')).not.toBeNull();
    });

    it('exact match returns a non-null result', () => {
        const result = fuzzyMatch('hello', 'hello');
        expect(result).not.toBeNull();
        expect(result!.ranges).toEqual([[0, 5]]);
    });

    it('query longer than text returns null', () => {
        expect(fuzzyMatch('toolong', 'short')).toBeNull();
    });
});
