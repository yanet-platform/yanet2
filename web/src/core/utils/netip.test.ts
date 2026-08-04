import { describe, it, expect } from 'vitest';
import {
    ipAddressToString,
    stringToIPAddress,
    ipRangeToCIDRs,
    cidrToIPRange,
    parseCIDRPrefix,
    parseCidrsToIPNets,
    parseIPToBytes,
    IPv4Prefix,
    CIDRParseError,
} from './netip';

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

describe('cidrToIPRange', () => {
    it('converts an IPv4 /24 to its start/end range', () => {
        expect(cidrToIPRange('10.0.0.0/24')).toEqual({ start: '10.0.0.0', end: '10.0.0.255' });
    });

    it('converts an IPv6 /64 to its start/end range', () => {
        expect(cidrToIPRange('2a02:6b8:2:d::/64'))
            .toEqual({ start: '2a02:6b8:2:d::', end: '2a02:6b8:2:d:ffff:ffff:ffff:ffff' });
    });

    it('converts an IPv4 /32 single address to a single-address range', () => {
        expect(cidrToIPRange('10.0.0.1/32')).toEqual({ start: '10.0.0.1', end: '10.0.0.1' });
    });

    it('converts an IPv6 /128 single address to a single-address range', () => {
        expect(cidrToIPRange('2001:db8::1/128')).toEqual({ start: '2001:db8::1', end: '2001:db8::1' });
    });

    it('masks host bits set in the address to the network base', () => {
        expect(cidrToIPRange('10.0.0.5/24')).toEqual({ start: '10.0.0.0', end: '10.0.0.255' });
    });

    it('masks host bits set in an IPv6 address to the network base', () => {
        expect(cidrToIPRange('2001:db8::1/32')).toEqual({ start: '2001:db8::', end: '2001:db8:ffff:ffff:ffff:ffff:ffff:ffff' });
    });

    it('returns undefined for an unparseable CIDR', () => {
        expect(cidrToIPRange('not-a-cidr')).toBeUndefined();
    });

    it('returns undefined for a missing prefix length', () => {
        expect(cidrToIPRange('10.0.0.0')).toBeUndefined();
    });

    it('returns undefined for an out-of-range prefix length', () => {
        expect(cidrToIPRange('10.0.0.0/33')).toBeUndefined();
    });

    it('converts a NAT64-style embedded-IPv4 CIDR using the RFC 4291 tail, not hex digits', () => {
        expect(cidrToIPRange('64:ff9b::10.0.0.0/120'))
            .toEqual({ start: '64:ff9b::a00:0', end: '64:ff9b::a00:ff' });
    });

    it('converts an IPv4-mapped embedded-IPv4 CIDR (::ffff: prefix)', () => {
        expect(cidrToIPRange('::ffff:10.0.0.0/120'))
            .toEqual({ start: '::ffff:a00:0', end: '::ffff:a00:ff' });
    });

    it('converts an IPv4-mapped embedded-IPv4 CIDR with a non-trivial dotted quad', () => {
        expect(cidrToIPRange('::ffff:192.168.1.0/120'))
            .toEqual({ start: '::ffff:c0a8:100', end: '::ffff:c0a8:1ff' });
    });

    it('converts an embedded-IPv4 tail written without :: compression', () => {
        expect(cidrToIPRange('1:2:3:4:5:6:10.0.0.1/128'))
            .toEqual({ start: '1:2:3:4:5:6:a00:1', end: '1:2:3:4:5:6:a00:1' });
    });

    it('converts :: compression combined with an embedded-IPv4 tail', () => {
        expect(cidrToIPRange('1::10.0.0.1/128'))
            .toEqual({ start: '1::a00:1', end: '1::a00:1' });
    });
});

describe('parseIPToBytes octet strictness', () => {
    it('accepts a plain valid IPv4 address', () => {
        expect(parseIPToBytes('10.0.0.1')).toEqual([10, 0, 0, 1]);
    });

    it('rejects an IPv4 octet with trailing junk', () => {
        expect(parseIPToBytes('10.0.0.1abc')).toBeUndefined();
    });

    it('accepts a valid embedded-IPv4 tail without :: compression', () => {
        expect(parseIPToBytes('1:2:3:4:5:6:10.0.0.1'))
            .toEqual([0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 10, 0, 0, 1]);
    });

    it('rejects an embedded-IPv4 tail with trailing junk in the last octet', () => {
        expect(parseIPToBytes('1:2:3:4:5:6:10.0.0.1abc')).toBeUndefined();
    });

    it('accepts a compressed embedded-IPv4 tail', () => {
        expect(parseIPToBytes('::10.0.0.1'))
            .toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 0, 0, 1]);
    });

    it('rejects a compressed embedded-IPv4 tail with trailing junk', () => {
        expect(parseIPToBytes('::10.0.0.1abc')).toBeUndefined();
    });

    it('rejects an out-of-range octet even with junk suppressed', () => {
        expect(parseIPToBytes('::999.0.0.1')).toBeUndefined();
    });
});

describe('parseIPv6ToBytes hex-group strictness', () => {
    it('rejects a first-position group with trailing junk', () => {
        expect(parseIPToBytes('1abcg::')).toBeUndefined();
    });

    it('rejects a non-first-position group with trailing junk', () => {
        expect(parseIPToBytes('1:2:3:4:5:6:7:8abcg')).toBeUndefined();
    });

    it('accepts :: alone (positive control)', () => {
        expect(parseIPToBytes('::')).toEqual(new Array(16).fill(0));
    });

    it('accepts ::1 (positive control)', () => {
        expect(parseIPToBytes('::1')).toEqual([
            0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1,
        ]);
    });

    it('accepts 1:: (positive control)', () => {
        expect(parseIPToBytes('1::')).toEqual([
            0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        ]);
    });

    it('accepts a full eight-group address (positive control)', () => {
        expect(parseIPToBytes('1:2:3:4:5:6:7:8')).toEqual([
            0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 7, 0, 8,
        ]);
    });

    it('accepts the already-covered embedded-IPv4 forms (positive control)', () => {
        expect(parseIPToBytes('::10.0.0.1'))
            .toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 0, 0, 1]);
        expect(parseIPToBytes('1:2:3:4:5:6:10.0.0.1'))
            .toEqual([0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 10, 0, 0, 1]);
    });
});

describe('parseCidrsToIPNets hex-group strictness', () => {
    it('drops an entry with trailing junk in the first group', () => {
        expect(parseCidrsToIPNets(['1abcg::/64'])).toEqual([]);
    });

    it('drops an entry with trailing junk in a non-first group', () => {
        expect(parseCidrsToIPNets(['1:2:3:4:5:6:7:8abcg/128'])).toEqual([]);
    });

    it('keeps well-formed entries alongside a dropped junk-group one', () => {
        expect(parseCidrsToIPNets(['1abcg::/64', '2001:db8::/32']))
            .toEqual(parseCidrsToIPNets(['2001:db8::/32']));
        expect(parseCidrsToIPNets(['2001:db8::/32'])).toHaveLength(1);
    });
});

describe('IPv4Prefix.parse mask strictness', () => {
    it('accepts a well-formed mask (positive control)', () => {
        const result = IPv4Prefix.parse('10.0.0.0/24');
        expect(result.ok).toBe(true);
        if (result.ok) {
            expect(result.value.prefixLength).toBe(24);
        }
    });

    it('rejects a mask with trailing alphabetic junk', () => {
        const result = IPv4Prefix.parse('10.0.0.0/24abc');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('rejects a mask with a trailing decimal fraction', () => {
        const result = IPv4Prefix.parse('10.0.0.0/24.5');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('rejects a mask with embedded whitespace', () => {
        const result = IPv4Prefix.parse('10.0.0.0/ 24');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('rejects a mask with an explicit leading plus sign', () => {
        const result = IPv4Prefix.parse('10.0.0.0/+24');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('rejects an empty mask', () => {
        const result = IPv4Prefix.parse('10.0.0.0/');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('still correctly rejects a negative mask', () => {
        const result = IPv4Prefix.parse('10.0.0.0/-1');
        expect(result).toEqual({ ok: false, error: CIDRParseError.InvalidPrefixLength });
    });

    it('parseCIDRPrefix (the general entry point) rejects the same malformed masks', () => {
        expect(parseCIDRPrefix('10.0.0.0/24abc').ok).toBe(false);
        expect(parseCIDRPrefix('10.0.0.0/24.5').ok).toBe(false);
    });

    it('rejects a malformed mask that also feeds cidrToIPRange, so a typo cannot silently install a route', () => {
        expect(cidrToIPRange('10.0.0.0/24abc')).toBeUndefined();
        expect(cidrToIPRange('10.0.0.0/24.5')).toBeUndefined();
    });
});

describe('parseCidrsToIPNets mask strictness', () => {
    it('converts a well-formed CIDR list (positive control)', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/24'])).toEqual([
            { addr: 'CgAAAA==', mask: '////AA==' },
        ]);
    });

    it('drops an entry with trailing alphabetic junk in the mask', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/24abc'])).toEqual([]);
    });

    it('drops an entry with a trailing decimal fraction in the mask', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/24.5'])).toEqual([]);
    });

    it('drops an entry with an explicit leading plus sign in the mask', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/+24'])).toEqual([]);
    });

    it('drops an entry with an empty mask', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/'])).toEqual([]);
    });

    it('keeps well-formed entries alongside dropped malformed ones', () => {
        expect(parseCidrsToIPNets(['10.0.0.0/24abc', '10.0.0.0/24'])).toEqual([
            { addr: 'CgAAAA==', mask: '////AA==' },
        ]);
    });

    it('drops an entry with trailing junk in an embedded-IPv4 tail octet', () => {
        expect(parseCidrsToIPNets(['1:2:3:4:5:6:10.0.0.1abc/128'])).toEqual([]);
    });

    it('keeps a valid embedded-IPv4 CIDR alongside a dropped junk-tail one', () => {
        expect(parseCidrsToIPNets(['1:2:3:4:5:6:10.0.0.1abc/128', '64:ff9b::10.0.0.0/120']))
            .toEqual(parseCidrsToIPNets(['64:ff9b::10.0.0.0/120']));
        expect(parseCidrsToIPNets(['64:ff9b::10.0.0.0/120'])).toHaveLength(1);
    });
});
