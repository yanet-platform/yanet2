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
