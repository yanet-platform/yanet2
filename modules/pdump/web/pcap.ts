import type { CapturedPacket } from './types';

const PCAP_GLOBAL_HEADER_BYTES = 24;
const PCAP_PACKET_HEADER_BYTES = 16;
const PCAP_LINKTYPE_ETHERNET = 1;

// Standard nanosecond-resolution pcap magic (libpcap's NSEC_TCPDUMP_MAGIC).
//
// The microsecond magic 0xa1b2c3d4 carries a microsecond fractional field, so
// the magic and the fractional units below must change together.
//
// Matches the Rust CLI's pcap_file::TsResolution::NanoSecond writer so both
// exports carry the same pcap variant. The CLI reads the dataplane's
// protobuf record directly and is exact to 1 ns; this writer's precision is
// bounded by the JSON transport (see the doc comment below).
const PCAP_MAGIC_NANOSECOND = 0xa1b23c4d;

const NANOS_PER_SECOND = 1000000000n;
const NANOS_PER_MILLISECOND = 1000000;

/**
 * Builds a pcap file buffer from captured packets.
 *
 * Per-packet timestamps use the transport-delivered epoch-nanosecond value
 * when available, falling back to the packet's arrival Date when it is
 * absent. Reading meta.timestamp instead of the Date avoids the Date's
 * truncation to whole milliseconds.
 *
 * The value is split into seconds and nanoseconds via BigInt rather than
 * double arithmetic. This keeps the split exact by construction, independent
 * of whether the input's magnitude happens to fall in a range where double
 * division and modulo are exact; it does not recover precision that a double
 * split would lose at typical epoch-nanosecond magnitudes.
 *
 * The gateway's JSON transport already rounds the value to the nearest
 * representable double before it reaches this code, so the resolution here
 * is bounded to roughly ±128 ns rather than exact.
 */
export const createPcapBuffer = (records: CapturedPacket[]): ArrayBuffer => {
    let totalSize = PCAP_GLOBAL_HEADER_BYTES;
    for (const packet of records) {
        totalSize += PCAP_PACKET_HEADER_BYTES + packet.parsed.raw.length;
    }

    const buffer = new ArrayBuffer(totalSize);
    const view = new DataView(buffer);
    const bytes = new Uint8Array(buffer);

    view.setUint32(0, PCAP_MAGIC_NANOSECOND, true);
    view.setUint16(4, 2, true);
    view.setUint16(6, 4, true);
    view.setInt32(8, 0, true);
    view.setUint32(12, 0, true);
    view.setUint32(16, 65535, true);
    view.setUint32(20, PCAP_LINKTYPE_ETHERNET, true);

    let offset = PCAP_GLOBAL_HEADER_BYTES;
    for (const packet of records) {
        const payload = packet.parsed.raw;
        const capturedLength = payload.length;
        const originalLength = packet.record.meta?.packet_len ?? capturedLength;

        let tsSec: number;
        let tsNsec: number;
        const rawTimestamp = packet.record.meta?.timestamp;
        if (rawTimestamp) {
            const ns = BigInt(rawTimestamp);
            tsSec = Number(ns / NANOS_PER_SECOND);
            tsNsec = Number(ns % NANOS_PER_SECOND);
        } else {
            const timestampMs = packet.timestamp.getTime();
            tsSec = Math.floor(timestampMs / 1000);
            tsNsec = (timestampMs % 1000) * NANOS_PER_MILLISECOND;
        }

        view.setUint32(offset, tsSec, true);
        view.setUint32(offset + 4, tsNsec, true);
        view.setUint32(offset + 8, capturedLength, true);
        view.setUint32(offset + 12, originalLength, true);
        offset += PCAP_PACKET_HEADER_BYTES;

        bytes.set(payload, offset);
        offset += capturedLength;
    }

    return buffer;
};
