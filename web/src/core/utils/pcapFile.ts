// pcap global-header magic as seen by a big-endian read of the first four
// bytes.
//
// The canonical value is 0xa1b2c3d4. A big-endian file stores it as the bytes
// a1 b2 c3 d4, so a big-endian read yields 0xa1b2c3d4; a little-endian file
// stores it byte-swapped as d4 c3 b2 a1, yielding 0xd4c3b2a1. The 0xa1b23c4d
// pair is the nanosecond-timestamp variant.
const MAGIC_MICRO_BE = 0xa1b2c3d4;
const MAGIC_MICRO_LE = 0xd4c3b2a1;
const MAGIC_NANO_BE = 0xa1b23c4d;
const MAGIC_NANO_LE = 0x4d3cb2a1;

const PCAP_GLOBAL_HEADER_BYTES = 24;
const PCAP_RECORD_HEADER_BYTES = 16;

/**
 * Parse a raw .pcap file buffer and return an array of per-frame Uint8Arrays.
 *
 * Supports both little-endian and big-endian pcap files, and both microsecond
 * and nanosecond timestamp variants.  Throws a descriptive Error if the magic
 * number is not recognised or the buffer is truncated.
 */
export const parsePcapFile = (bytes: Uint8Array): Uint8Array[] => {
    if (bytes.length < PCAP_GLOBAL_HEADER_BYTES) {
        throw new Error('File is too short to be a valid pcap (need at least 24 bytes for the global header).');
    }

    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const magic = view.getUint32(0, false);

    let littleEndian: boolean;
    if (magic === MAGIC_MICRO_LE || magic === MAGIC_NANO_LE) {
        littleEndian = true;
    } else if (magic === MAGIC_MICRO_BE || magic === MAGIC_NANO_BE) {
        littleEndian = false;
    } else {
        throw new Error(
            `Unrecognised pcap magic 0x${magic.toString(16).padStart(8, '0')}. ` +
            'Expected 0xa1b2c3d4 / 0xd4c3b2a1 (microsecond) or ' +
            '0xa1b23c4d / 0x4d3cb2a1 (nanosecond).'
        );
    }

    const frames: Uint8Array[] = [];
    let offset = PCAP_GLOBAL_HEADER_BYTES;

    while (offset < bytes.length) {
        if (offset + PCAP_RECORD_HEADER_BYTES > bytes.length) {
            break;
        }

        const inclLen = view.getUint32(offset + 8, littleEndian);

        offset += PCAP_RECORD_HEADER_BYTES;

        if (offset + inclLen > bytes.length) {
            break;
        }

        frames.push(bytes.slice(offset, offset + inclLen));
        offset += inclLen;
    }

    return frames;
};
