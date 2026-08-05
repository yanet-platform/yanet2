import { describe, expect, it } from 'vitest';
import { parsePacket } from './packetParser';

const fromHex = (hex: string): Uint8Array => {
    const normalized = hex.replace(/\s+/g, '');
    const bytes: number[] = [];
    for (let idx = 0; idx < normalized.length; idx += 2) {
        bytes.push(parseInt(normalized.slice(idx, idx + 2), 16));
    }
    return Uint8Array.from(bytes);
};

// Builds an Ethernet + bare IPv6 header packet with the given src/dst
// 16-bit groups, for exercising IPv6 address rendering through parsePacket.
const buildIPv6Packet = (srcGroups: number[], dstGroups: number[]): Uint8Array => {
    const bytes: number[] = [
        0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // dst mac
        0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, // src mac
        0x86, 0xdd, // etherType IPv6
        0x60, 0x00, 0x00, 0x00, // version 6, traffic class 0, flow label 0
        0x00, 0x00, // payload length
        0x3b, // next header (59, no next header)
        0x40, // hop limit
    ];
    for (const group of [...srcGroups, ...dstGroups]) {
        bytes.push((group >> 8) & 0xff, group & 0xff);
    }
    return Uint8Array.from(bytes);
};

describe('parsePacket VLAN parsing', () => {
    it('parses a single VLAN-tagged IPv4 packet and keeps IPv4 offsets correct', () => {
        const packet = fromHex(`
            00 11 22 33 44 55 66 77 88 99 aa bb 81 00
            b0 64 08 00
            45 00 00 14 12 34 40 00 40 11 00 00 c0 a8 01 01 c0 a8 01 02
        `);

        const parsed = parsePacket(packet);

        expect(parsed.vlans).toHaveLength(1);
        expect(parsed.vlans?.[0]).toMatchObject({
            tpid: 0x8100,
            tpidName: '802.1Q',
            tci: 0xb064,
            pcp: 5,
            dei: true,
            vlanId: 100,
            innerEtherType: 0x0800,
            innerEtherTypeName: 'IPv4',
        });
        expect(parsed.ipv4?.srcAddr).toBe('192.168.1.1');
        expect(parsed.ipv4?.dstAddr).toBe('192.168.1.2');
        expect(parsed.payloadOffset).toBe(38);
    });

    it('parses stacked VLAN tags and uses final inner EtherType for IPv4', () => {
        const packet = fromHex(`
            00 11 22 33 44 55 66 77 88 99 aa bb 88 a8
            60 c8 81 00
            31 2c 08 00
            45 00 00 14 12 34 40 00 40 ff 00 00 0a 00 00 01 0a 00 00 02
        `);

        const parsed = parsePacket(packet);

        expect(parsed.vlans).toHaveLength(2);
        expect(parsed.vlans?.[0]).toMatchObject({
            tpid: 0x88a8,
            pcp: 3,
            dei: false,
            vlanId: 200,
            innerEtherType: 0x8100,
            innerEtherTypeName: '802.1Q',
        });
        expect(parsed.vlans?.[1]).toMatchObject({
            tpid: 0x8100,
            pcp: 1,
            dei: true,
            vlanId: 300,
            innerEtherType: 0x0800,
            innerEtherTypeName: 'IPv4',
        });
        expect(parsed.ipv4?.srcAddr).toBe('10.0.0.1');
        expect(parsed.ipv4?.dstAddr).toBe('10.0.0.2');
        expect(parsed.payloadOffset).toBe(42);
    });
});

describe('parsePacket IPv6 address compression', () => {
    it('compresses the longest zero run over an earlier shorter one', () => {
        const packet = buildIPv6Packet(
            [1, 0, 0, 2, 0, 0, 0, 3],
            [0, 0, 1, 0, 0, 0, 0, 2],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('1:0:0:2::3');
        expect(parsed.ipv6?.dstAddr).toBe('0:0:1::2');
    });

    it('compresses a zero run that reaches the last group', () => {
        const packet = buildIPv6Packet(
            [1, 2, 3, 4, 5, 0, 0, 0],
            [1, 2, 3, 4, 5, 0, 0, 0],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('1:2:3:4:5::');
    });

    it('compresses the all-zero address to ::', () => {
        const packet = buildIPv6Packet(
            [0, 0, 0, 0, 0, 0, 0, 0],
            [0, 0, 0, 0, 0, 0, 0, 0],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('::');
    });

    it('leaves a single zero group uncompressed', () => {
        const packet = buildIPv6Packet(
            [1, 0, 2, 3, 4, 5, 6, 7],
            [1, 0, 2, 3, 4, 5, 6, 7],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('1:0:2:3:4:5:6:7');
    });

    it('breaks an equal-length zero-run tie in favor of the first run', () => {
        const packet = buildIPv6Packet(
            [1, 0, 0, 2, 0, 0, 3, 4],
            [1, 0, 0, 2, 0, 0, 3, 4],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('1::2:0:0:3:4');
    });

    it('leaves an address with no zero group untouched', () => {
        const packet = buildIPv6Packet(
            [1, 2, 3, 4, 5, 6, 7, 8],
            [1, 2, 3, 4, 5, 6, 7, 8],
        );

        const parsed = parsePacket(packet);

        expect(parsed.ipv6?.srcAddr).toBe('1:2:3:4:5:6:7:8');
    });
});
