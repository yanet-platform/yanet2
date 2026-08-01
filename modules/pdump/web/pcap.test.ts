import { describe, expect, it } from 'vitest';
import { createPcapBuffer } from './pcap';
import type { CapturedPacket } from './types';

const makePacket = (options: {
    raw: Uint8Array;
    timestamp: Date;
    metaTimestamp?: number;
    packetLen?: number;
}): CapturedPacket => ({
    id: 0,
    timestamp: options.timestamp,
    record: {
        meta: {
            timestamp: options.metaTimestamp,
            packet_len: options.packetLen,
        },
    },
    parsed: {
        payloadOffset: 0,
        payloadLength: options.raw.length,
        raw: options.raw,
    },
});

const readGlobalHeader = (buffer: ArrayBuffer) => {
    const view = new DataView(buffer);
    return {
        magic: view.getUint32(0, true),
        versionMajor: view.getUint16(4, true),
        versionMinor: view.getUint16(6, true),
        snaplen: view.getUint32(16, true),
        linktype: view.getUint32(20, true),
    };
};

const readPacketHeader = (buffer: ArrayBuffer, offset: number) => {
    const view = new DataView(buffer);
    return {
        tsSec: view.getUint32(offset, true),
        tsFrac: view.getUint32(offset + 4, true),
        inclLen: view.getUint32(offset + 8, true),
        origLen: view.getUint32(offset + 12, true),
    };
};

// meta.timestamp is typed number | string (RecordMeta in web/src/core/api/pdump.ts).
// The current gateway sends it as a bare JSON number: encoding/json marshals the
// wire uint64 as a plain number, and JSON.parse rounds it to the nearest
// representable double (~128 ns at epoch-nanosecond magnitude). The numeric
// literals below are exact doubles chosen so the fixtures reflect what
// production actually delivers, with no silent rounding hidden in the test
// source.
describe('createPcapBuffer', () => {
    it('keeps sub-millisecond resolution the arrival Date would truncate', () => {
        const packet = makePacket({
            raw: new Uint8Array([1, 2, 3]),
            timestamp: new Date(1754049600123),
            metaTimestamp: 1754049600123456768,
        });

        const buffer = createPcapBuffer([packet]);
        const header = readPacketHeader(buffer, 24);

        expect(header.tsSec).toBe(1754049600);
        expect(header.tsFrac).toBe(123456768);
    });

    it('handles a zero nanosecond remainder', () => {
        const packet = makePacket({
            raw: new Uint8Array([1]),
            timestamp: new Date(1754049600000),
            metaTimestamp: 1754049600000000000,
        });

        const buffer = createPcapBuffer([packet]);
        const header = readPacketHeader(buffer, 24);

        expect(header.tsSec).toBe(1754049600);
        expect(header.tsFrac).toBe(0);
    });

    it('handles a remainder just below the second boundary', () => {
        const packet = makePacket({
            raw: new Uint8Array([1]),
            timestamp: new Date(1754049600999),
            metaTimestamp: 1754049600999998976,
        });

        const buffer = createPcapBuffer([packet]);
        const header = readPacketHeader(buffer, 24);

        expect(header.tsSec).toBe(1754049600);
        expect(header.tsFrac).toBe(999998976);
    });

    it('writes a nanosecond-resolution pcap global header', () => {
        const packet = makePacket({
            raw: new Uint8Array([1, 2]),
            timestamp: new Date(1754049600123),
            metaTimestamp: 1754049600123456768,
        });

        const buffer = createPcapBuffer([packet]);
        const header = readGlobalHeader(buffer);

        expect(header.magic).toBe(0xa1b23c4d);
        expect(header.versionMajor).toBe(2);
        expect(header.versionMinor).toBe(4);
        expect(header.snaplen).toBe(65535);
        expect(header.linktype).toBe(1);
    });

    it('falls back to the arrival Date when the dataplane timestamp is absent', () => {
        const packet = makePacket({
            raw: new Uint8Array([1]),
            timestamp: new Date(1754049600123),
        });

        const buffer = createPcapBuffer([packet]);
        const header = readPacketHeader(buffer, 24);

        expect(header.tsSec).toBe(1754049600);
        expect(header.tsFrac).toBe(123000000);
    });

    it('writes payload and lengths correctly across multiple records', () => {
        const rawA = new Uint8Array([1, 2, 3]);
        const rawB = new Uint8Array([4, 5]);
        const packetA = makePacket({
            raw: rawA,
            timestamp: new Date(1754049600000),
            metaTimestamp: 1754049600000000000,
            packetLen: 10,
        });
        const packetB = makePacket({
            raw: rawB,
            timestamp: new Date(1754049601000),
            metaTimestamp: 1754049601000000000,
        });

        const buffer = createPcapBuffer([packetA, packetB]);

        expect(buffer.byteLength).toBe(24 + (16 + rawA.length) + (16 + rawB.length));

        const headerA = readPacketHeader(buffer, 24);
        expect(headerA.inclLen).toBe(rawA.length);
        expect(headerA.origLen).toBe(10);

        const bytes = new Uint8Array(buffer);
        expect(Array.from(bytes.slice(24 + 16, 24 + 16 + rawA.length))).toEqual(Array.from(rawA));

        const offsetB = 24 + 16 + rawA.length;
        const headerB = readPacketHeader(buffer, offsetB);
        expect(headerB.inclLen).toBe(rawB.length);
        expect(headerB.origLen).toBe(rawB.length);

        expect(
            Array.from(bytes.slice(offsetB + 16, offsetB + 16 + rawB.length))
        ).toEqual(Array.from(rawB));
    });
});
