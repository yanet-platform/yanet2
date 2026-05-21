import { describe, it, expect } from 'vitest';
import { ipAddressToString, stringToIPAddress } from './netip';

describe('ipAddressToString', () => {
    it('returns the string directly for IPv4 wire form', () => {
        expect(ipAddressToString({ addr: '10.0.0.1' })).toBe('10.0.0.1');
    });

    it('returns the string directly for IPv6 wire form', () => {
        expect(ipAddressToString({ addr: '2001:db8::1' })).toBe('2001:db8::1');
    });

    it('returns the string directly for link-local IPv6', () => {
        expect(ipAddressToString({ addr: 'fe80::1' })).toBe('fe80::1');
    });

    it('handles number[] bytes as defensive fallback (IPv4)', () => {
        expect(ipAddressToString({ addr: [10, 0, 0, 1] })).toBe('10.0.0.1');
    });

    it('handles Uint8Array bytes as defensive fallback (IPv4)', () => {
        expect(ipAddressToString({ addr: new Uint8Array([10, 0, 0, 1]) })).toBe('10.0.0.1');
    });

    it('returns empty string for undefined', () => {
        expect(ipAddressToString(undefined)).toBe('');
    });

    it('returns empty string for missing addr', () => {
        expect(ipAddressToString({})).toBe('');
    });
});

describe('stringToIPAddress', () => {
    it('encodes a valid IPv4 address', () => {
        expect(stringToIPAddress('10.0.0.1')).toEqual({ addr: '10.0.0.1' });
    });

    it('encodes a valid IPv6 address', () => {
        expect(stringToIPAddress('2001:db8::1')).toEqual({ addr: '2001:db8::1' });
    });

    it('returns undefined for an invalid address', () => {
        expect(stringToIPAddress('not-an-ip')).toBeUndefined();
    });

    it('returns undefined for an empty string', () => {
        expect(stringToIPAddress('')).toBeUndefined();
    });
});
