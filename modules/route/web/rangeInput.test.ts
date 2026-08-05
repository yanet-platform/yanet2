import { describe, it, expect } from 'vitest';
import { formatRangeInput, isValidRangeInput, parseRangeInput } from './rangeInput';

describe('parseRangeInput', () => {
    it('normalizes a CIDR to its from/to endpoints', () => {
        expect(parseRangeInput('10.0.0.0/24')).toEqual({ start: '10.0.0.0', end: '10.0.0.255' });
    });

    it('normalizes an explicit "from - to" range', () => {
        expect(parseRangeInput('10.0.0.1 - 10.0.0.130')).toEqual({ start: '10.0.0.1', end: '10.0.0.130' });
    });

    it('treats a bare host address as a single-address range', () => {
        expect(parseRangeInput('10.0.0.5')).toEqual({ start: '10.0.0.5', end: '10.0.0.5' });
    });

    it('rejects a reversed range', () => {
        expect(parseRangeInput('10.0.0.130 - 10.0.0.1')).toBeUndefined();
    });

    it('rejects garbage input', () => {
        expect(parseRangeInput('not an ip')).toBeUndefined();
    });
});

describe('isValidRangeInput', () => {
    it('mirrors parseRangeInput success/failure', () => {
        expect(isValidRangeInput('10.0.0.0/24')).toBe(true);
        expect(isValidRangeInput('garbage')).toBe(false);
    });
});

describe('formatRangeInput', () => {
    it('formats a CIDR-aligned range as a CIDR', () => {
        expect(formatRangeInput('10.0.0.0', '10.0.0.255')).toBe('10.0.0.0/24');
    });

    it('formats a non-aligned range as "from - to"', () => {
        expect(formatRangeInput('10.0.0.1', '10.0.0.130')).toBe('10.0.0.1 - 10.0.0.130');
    });
});
