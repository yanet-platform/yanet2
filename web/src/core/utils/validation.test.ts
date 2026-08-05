import { describe, it, expect } from 'vitest';
import { isValidCidr, isValidCidrPrefix } from './validation';

describe('isValidCidr / isValidCidrPrefix — RFC 4291 embedded-IPv4 forms', () => {
    const embeddedIPv4Prefixes = [
        '64:ff9b::10.0.0.0/120',
        '::ffff:192.168.1.1/128',
        '1:2:3:4:5:6:10.0.0.1/128',
        '1::10.0.0.1/128',
    ];

    for (const prefix of embeddedIPv4Prefixes) {
        it(`accepts "${prefix}" (both functions)`, () => {
            expect(isValidCidr(prefix)).toBe(true);
            expect(isValidCidrPrefix(prefix)).toBe(true);
        });
    }

    it('accepts mask-less embedded-IPv4 via isValidCidr', () => {
        expect(isValidCidr('::ffff:192.168.1.1')).toBe(true);
    });

    it('rejects mask-less embedded-IPv4 via isValidCidrPrefix (mask mandatory)', () => {
        expect(isValidCidrPrefix('::ffff:192.168.1.1')).toBe(false);
    });
});

describe('isValidCidr / isValidCidrPrefix — still accepted', () => {
    const prefixes = ['10.0.0.0/8', '0.0.0.0/0', '::/0', '2001:db8::/32'];

    for (const prefix of prefixes) {
        it(`accepts "${prefix}" (both functions)`, () => {
            expect(isValidCidr(prefix)).toBe(true);
            expect(isValidCidrPrefix(prefix)).toBe(true);
        });
    }

    it('accepts a bare IPv4 host address via isValidCidr', () => {
        expect(isValidCidr('192.168.1.1')).toBe(true);
    });

    it('accepts a bare IPv6 host address via isValidCidr', () => {
        expect(isValidCidr('2001:db8::1')).toBe(true);
    });

    it('rejects a bare host address via isValidCidrPrefix (mask mandatory)', () => {
        expect(isValidCidrPrefix('192.168.1.1')).toBe(false);
    });
});

describe('isValidCidr / isValidCidrPrefix — rejected', () => {
    const invalid = [
        '',
        '   ',
        '1.2.3.4/33',
        '2001:db8::/129',
        '1:2',
        '2001:',
        '999.0.0.0/8',
        '64:ff9b::10.0.0/120', // three-octet embedded IPv4 tail
        'fe80::1%eth0',
    ];

    for (const s of invalid) {
        it(`rejects ${JSON.stringify(s)} (both functions)`, () => {
            expect(isValidCidr(s)).toBe(false);
            expect(isValidCidrPrefix(s)).toBe(false);
        });
    }

    // A leading-zero IPv4 octet is rejected, matching IPv4Address.parse.
    it('rejects a leading-zero IPv4 octet', () => {
        expect(isValidCidr('010.0.0.1/24')).toBe(false);
        expect(isValidCidrPrefix('010.0.0.1/24')).toBe(false);
    });

    // A zero-padded prefix length is rejected. The control plane reparses the
    // prefix string with Go's netip.ParsePrefix, which refuses the padding.
    it('rejects a zero-padded prefix length', () => {
        expect(isValidCidr('10.0.0.0/024')).toBe(false);
        expect(isValidCidrPrefix('10.0.0.0/024')).toBe(false);
        expect(isValidCidr('2001:db8::/0128')).toBe(false);
        expect(isValidCidrPrefix('2001:db8::/0128')).toBe(false);
    });
});

describe('isValidCidr / isValidCidrPrefix — whitespace trimming', () => {
    // Witness for useFIBDraft.ts's rowsToFIBEntries comment, which asserts
    // isValidCidrPrefix is looser than cidrToIPRange (which does not trim).
    it('trims surrounding whitespace before validating', () => {
        expect(isValidCidrPrefix(' 1.2.3.4/24 ')).toBe(true);
        expect(isValidCidr(' 1.2.3.4/24 ')).toBe(true);
    });
});
