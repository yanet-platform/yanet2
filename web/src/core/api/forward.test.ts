import { describe, it, expect } from 'vitest';
import { ForwardMode, parseForwardMode } from './forward';

describe('parseForwardMode', () => {
    it('passes a named mode through unchanged', () => {
        expect(parseForwardMode(ForwardMode.OUT)).toBe(ForwardMode.OUT);
    });

    it('maps a declared number from an older gateway to its name', () => {
        expect(parseForwardMode(1)).toBe(ForwardMode.IN);
        expect(parseForwardMode(2)).toBe(ForwardMode.OUT);
    });

    it('reads an undeclared number and an absent mode as NONE', () => {
        expect(parseForwardMode(7)).toBe(ForwardMode.NONE);
        expect(parseForwardMode(undefined)).toBe(ForwardMode.NONE);
    });

    it('reads a name this build does not declare as NONE', () => {
        expect(parseForwardMode('LOOPBACK' as ForwardMode)).toBe(ForwardMode.NONE);
    });
});
