import React, { useMemo } from 'react';
import { PacketTable as SharedPacketTable } from '@yanet/core/components/packet';
import type { PacketRow } from '@yanet/core/components/packet';
import type { CapturedPacket } from './types';

export interface PacketTableProps {
    packets: CapturedPacket[];
    selectedPacketId: number | null;
    onSelectPacket: (packet: CapturedPacket | null) => void;
    searchQuery: string;
    onSearchQueryChange: (value: string) => void;
    isCapturing: boolean;
    configName: string | null;
    onClearPackets: () => void;
    /** Set of packet IDs that are newly arrived (used for row-flash animation). */
    newPacketIds: Set<number>;
    /** Whether the view is paused (controlled by parent). */
    paused: boolean;
    /** Callback to toggle pause state in the parent. */
    onTogglePause: () => void;
    /** Whether auto-scroll is enabled (controlled by parent). */
    autoScroll: boolean;
    /** Callback when auto-scroll state changes. */
    onAutoScrollChange: (value: boolean) => void;
}

/**
 * Pdump-specific packet table adapter.
 *
 * Converts CapturedPacket[] (which carries pdump capture metadata) to the
 * generic PacketRow[] shape accepted by the shared PacketTable component,
 * then delegates all rendering.
 */
export const PacketTable: React.FC<PacketTableProps> = ({
    packets,
    selectedPacketId,
    onSelectPacket,
    searchQuery,
    onSearchQueryChange,
    isCapturing,
    configName,
    onClearPackets,
    newPacketIds,
    paused,
    onTogglePause,
    autoScroll,
    onAutoScrollChange,
}) => {
    const packetRows = useMemo((): PacketRow[] =>
        packets.map(p => ({ id: p.id, parsed: p.parsed, timestamp: p.timestamp })),
        [packets]
    );

    const handleSelectPacket = (row: PacketRow | null): void => {
        if (row === null) {
            onSelectPacket(null);
            return;
        }
        const original = packets.find(p => p.id === row.id) ?? null;
        onSelectPacket(original);
    };

    return (
        <SharedPacketTable
            key={configName ?? 'empty'}
            packets={packetRows}
            selectedPacketId={selectedPacketId}
            onSelectPacket={handleSelectPacket}
            searchQuery={searchQuery}
            onSearchQueryChange={onSearchQueryChange}
            newPacketIds={newPacketIds}
            paused={paused}
            onTogglePause={onTogglePause}
            autoScroll={autoScroll}
            onAutoScrollChange={onAutoScrollChange}
            onClearPackets={onClearPackets}
            isLive={isCapturing && configName !== null}
            emptyMessage={
                !configName
                    ? 'Select a config and start capture to see packets.'
                    : 'Waiting for packets matching the filter...'
            }
            emptyFilterMessage="No packets match the filter"
            showToolbarActions
            showTimeColumn
        />
    );
};
