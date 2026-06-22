import { describe, it, expect } from 'vitest';
import { parsePcapFile } from './pcapFile';

// Build a minimal, spec-faithful pcap byte buffer wrapping the given frames.
//
// A real pcap stores the magic value and record lengths in the file's own byte
// order, so endian 'le' lands the magic d4 c3 b2 a1 on disk while 'be' lands
// a1 b2 c3 d4. magicValue selects the microsecond (default) or nanosecond
// (0xa1b23c4d) variant.
const buildPcap = (
    frames: Uint8Array[],
    endian: 'le' | 'be' = 'le',
    magicValue = 0xa1b2c3d4,
): Uint8Array => {
    const little = endian === 'le';
    const total = 24 + frames.reduce((n, frame) => n + 16 + frame.length, 0);
    const bytes = new Uint8Array(total);
    const view = new DataView(bytes.buffer);

    view.setUint32(0, magicValue, little);

    let offset = 24;
    for (const frame of frames) {
        view.setUint32(offset + 8, frame.length, little);
        view.setUint32(offset + 12, frame.length, little);
        offset += 16;
        bytes.set(frame, offset);
        offset += frame.length;
    }
    return bytes;
};

describe('parsePcapFile', () => {
    it('parses little-endian frames preserving bytes and order', () => {
        const frames = [new Uint8Array([1, 2, 3]), new Uint8Array([9, 8, 7, 6])];
        const parsed = parsePcapFile(buildPcap(frames, 'le'));
        expect(parsed).toHaveLength(2);
        expect(Array.from(parsed[0])).toEqual([1, 2, 3]);
        expect(Array.from(parsed[1])).toEqual([9, 8, 7, 6]);
    });

    it('parses big-endian frames', () => {
        const parsed = parsePcapFile(buildPcap([new Uint8Array([0xaa, 0xbb])], 'be'));
        expect(parsed).toHaveLength(1);
        expect(Array.from(parsed[0])).toEqual([0xaa, 0xbb]);
    });

    it('parses a nanosecond-timestamp capture', () => {
        const parsed = parsePcapFile(buildPcap([new Uint8Array([5, 6, 7])], 'le', 0xa1b23c4d));
        expect(parsed).toHaveLength(1);
        expect(Array.from(parsed[0])).toEqual([5, 6, 7]);
    });

    it('returns no frames for a header-only file', () => {
        expect(parsePcapFile(buildPcap([]))).toHaveLength(0);
    });

    it('stops at a truncated trailing record instead of overreading', () => {
        const full = buildPcap([new Uint8Array([1, 2, 3, 4])]);
        // Drop the last two payload bytes: the record claims 4 but only 2 remain.
        const truncated = full.slice(0, full.length - 2);
        expect(parsePcapFile(truncated)).toHaveLength(0);
    });

    it('throws on a buffer shorter than the global header', () => {
        expect(() => parsePcapFile(new Uint8Array(10))).toThrow(/global header/);
    });

    it('throws on an unrecognised magic number', () => {
        const bytes = new Uint8Array(24);
        new DataView(bytes.buffer).setUint32(0, 0x12345678, false);
        expect(() => parsePcapFile(bytes)).toThrow(/magic/i);
    });
});
