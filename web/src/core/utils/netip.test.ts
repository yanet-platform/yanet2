import { describe, it, expect } from 'vitest';
import { ipAddressToString, stringToIPAddress, ipRangeToCIDRs } from './netip';

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

describe('ipRangeToCIDRs', () => {
    it('upper IPv6 half collapses to a single /1 block', () => {
        expect(ipRangeToCIDRs({ start: '8000::', end: 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff' }))
            .toEqual(['8000::/1']);
    });

    it('full IPv6 space collapses to ::/0', () => {
        expect(ipRangeToCIDRs({ start: '::', end: 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff' }))
            .toEqual(['::/0']);
    });

    it('single IPv4 /24 block is preserved', () => {
        expect(ipRangeToCIDRs({ start: '10.0.0.0', end: '10.0.0.255' }))
            .toEqual(['10.0.0.0/24']);
    });

    it('non-CIDR IPv6 range decomposes into two /64 blocks', () => {
        expect(ipRangeToCIDRs({ start: '2a02:6b8:2:d::', end: '2a02:6b8:2:e:ffff:ffff:ffff:ffff' }))
            .toEqual(['2a02:6b8:2:d::/64', '2a02:6b8:2:e::/64']);
    });

    it('returns [] for undefined input', () => {
        expect(ipRangeToCIDRs(undefined)).toEqual([]);
    });

    it('returns [] when start or end is missing', () => {
        expect(ipRangeToCIDRs({ start: '', end: '10.0.0.1' })).toEqual([]);
        expect(ipRangeToCIDRs({ start: '10.0.0.1', end: '' })).toEqual([]);
    });

    it('returns [] for invalid address strings', () => {
        expect(ipRangeToCIDRs({ start: 'not-an-ip', end: '10.0.0.1' })).toEqual([]);
    });

    it('returns [] when start and end belong to different families', () => {
        expect(ipRangeToCIDRs({ start: '10.0.0.1', end: '::1' })).toEqual([]);
    });

    it('returns [] when start is greater than end', () => {
        expect(ipRangeToCIDRs({ start: '10.0.0.255', end: '10.0.0.0' })).toEqual([]);
    });
});
