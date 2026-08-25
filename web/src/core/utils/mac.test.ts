import { describe, it, expect } from 'vitest';
import { normalizeMAC } from './mac';

describe('MAC address normalization', () => {
    it('keeps a canonical colon-separated address unchanged', () => {
        expect(normalizeMAC('3a:ac:26:9b:5b:f9')).toBe('3a:ac:26:9b:5b:f9');
    });

    it('lowercases hex digits and replaces hyphens with colons', () => {
        expect(normalizeMAC('3A-AC-26-9B-5B-F9')).toBe('3a:ac:26:9b:5b:f9');
    });

    it('expands the dotted layout into colon-separated octets', () => {
        expect(normalizeMAC('3aac.269b.5bf9')).toBe('3a:ac:26:9b:5b:f9');
    });

    it('expands the unseparated layout into colon-separated octets', () => {
        expect(normalizeMAC('3aac269b5bf9')).toBe('3a:ac:26:9b:5b:f9');
    });

    it('zero-pads single-digit octets', () => {
        expect(normalizeMAC('1:2:3:4:5:6')).toBe('01:02:03:04:05:06');
    });

    it('returns undefined for an empty input', () => {
        expect(normalizeMAC('')).toBeUndefined();
    });

    it('returns undefined for a malformed input', () => {
        expect(normalizeMAC('not-a-mac')).toBeUndefined();
    });
});
