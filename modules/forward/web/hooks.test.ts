import { describe, it, expect } from 'vitest';
import { effectiveCounterName, rulesToNgItems } from './hooks';
import { ForwardMode } from '@yanet/core/api/forward';

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

describe('rulesToNgItems', () => {
    it('reads a numeric mode from an older gateway as the named mode', () => {
        const [item] = rulesToNgItems([{ action: { target: 'eth0', mode: 2 } }]);
        expect(item.mode).toBe(ForwardMode.OUT);
    });

    it('reads a named mode as itself', () => {
        const [item] = rulesToNgItems([{ action: { target: 'eth0', mode: ForwardMode.IN } }]);
        expect(item.mode).toBe(ForwardMode.IN);
    });
});
