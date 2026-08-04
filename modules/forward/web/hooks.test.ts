import { describe, it, expect } from 'vitest';
import { effectiveCounterName } from './hooks';

describe('effectiveCounterName', () => {
    it('returns the explicit counter name verbatim when set', () => {
        expect(effectiveCounterName('my_counter', 'eth0')).toBe('my_counter');
    });

    it('materialises to_<target> when the counter is empty', () => {
        expect(effectiveCounterName('', 'eth0')).toBe('to_eth0');
    });

    it('materialises the literal to_ when both counter and target are empty', () => {
        expect(effectiveCounterName('', '')).toBe('to_');
    });
});
