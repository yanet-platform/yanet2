import React from 'react';
import { PacketInspector, Section, KV } from '@yanet/core/components/packet';
import type { CapturedPacket } from './types';

interface PacketDrawerProps {
    open: boolean;
    packet: CapturedPacket | null;
    packetIndex: number;
    totalPackets: number;
    configName?: string;
    onClose: () => void;
    onPrev: () => void;
    onNext: () => void;
}

const formatTime = (date: Date): string =>
    date.toLocaleTimeString('en-US', {
        hour12: false,
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        fractionalSecondDigits: 3,
    } as Intl.DateTimeFormatOptions);

/**
 * Right-side drawer for inspecting a captured pdump packet.
 *
 * Wraps the shared PacketInspector and injects capture metadata
 * (worker, queue, rx/tx device, timestamp) via the metaSection prop.
 */
const PacketDrawer: React.FC<PacketDrawerProps> = ({
    open,
    packet,
    packetIndex,
    totalPackets,
    configName,
    onClose,
    onPrev,
    onNext,
}) => {
    const packetRow = packet
        ? { id: packet.id, parsed: packet.parsed, timestamp: packet.timestamp }
        : null;

    const metaSection = packet ? (
        <Section kind="meta" title="Capture" sub={configName ?? undefined}>
            <KV k="Time" v={formatTime(packet.timestamp)} />
            <KV k="Worker" v={packet.record.meta?.worker_idx ?? 'N/A'} />
            <KV k="Queue" v={packet.record.meta?.queue ?? 'N/A'} />
            <KV k="RX device" v={packet.record.meta?.rx_device_id ?? 'N/A'} />
            <KV k="TX device" v={packet.record.meta?.tx_device_id ?? 'N/A'} />
            <KV
                k="Length"
                v={`${packet.record.meta?.packet_len ?? packet.parsed.raw.length} bytes (captured ${packet.record.meta?.data_size ?? packet.parsed.raw.length})`}
            />
        </Section>
    ) : undefined;

    return (
        <PacketInspector
            open={open}
            packet={packetRow}
            packetIndex={packetIndex}
            totalPackets={totalPackets}
            onClose={onClose}
            onPrev={onPrev}
            onNext={onNext}
            metaSection={metaSection}
        />
    );
};

export default React.memo(PacketDrawer);
