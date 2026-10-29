import { describe, it, expect } from 'vitest';
import { protocolEntriesToRanges } from './parseHelpers';

describe('protocolEntriesToRanges', () => {
    it('expands an unconstrained entry to the whole protocol block', () => {
        expect(protocolEntriesToRanges([{ number: 17 }])).toEqual([{ from: 17 << 8, to: (17 << 8) | 0xff }]);
    });

    it('expands an exact TCP flag byte to a single encoded value', () => {
        expect(protocolEntriesToRanges([{ number: 6, tcp: { flags: 0x02, mask: 0xff } }])).toEqual([
            { from: (6 << 8) | 0x02, to: (6 << 8) | 0x02 },
        ]);
    });

    it('opens one run per block for a single examined TCP flag bit', () => {
        const ranges = protocolEntriesToRanges([{ number: 6, tcp: { flags: 0x10, mask: 0x10 } }]);

        expect(ranges).toHaveLength(8);
        expect(ranges[0]).toEqual({ from: (6 << 8) | 0x10, to: (6 << 8) | 0x1f });
        expect(ranges[7]).toEqual({ from: (6 << 8) | 0xf0, to: (6 << 8) | 0xff });
    });

    it('maps ICMP and ICMPv6 types to their protocol blocks', () => {
        const ranges = protocolEntriesToRanges([
            { number: 1, icmp_types: [{ from: 8, to: 8 }] },
            { number: 58, icmp6_types: [{ from: 135, to: 136 }] },
        ]);

        expect(ranges).toEqual([
            { from: (1 << 8) | 8, to: (1 << 8) | 8 },
            { from: (58 << 8) | 135, to: (58 << 8) | 136 },
        ]);
    });

    it('treats an absent type bound as the proto3 zero, not the byte top', () => {
        // The gateway omits a `to: 0` from the wire JSON, so the entry
        // arrives as an empty object.
        expect(protocolEntriesToRanges([{ number: 1, icmp_types: [{}] }])).toEqual([
            { from: 1 << 8, to: 1 << 8 },
        ]);
    });

    it('skips entries whose number carries no protocol byte', () => {
        expect(protocolEntriesToRanges([{ number: 256 }])).toEqual([]);
        expect(protocolEntriesToRanges(undefined)).toEqual([]);
    });
});
