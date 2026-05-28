import React, { useRef, useMemo, useState, useCallback, useEffect } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { TextInput } from '@gravity-ui/uikit';
import { EmptyState } from '../../../components';
import { useContainerHeight } from '../../../hooks';
import { ROW_HEIGHT, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT, OVERSCAN, TOTAL_WIDTH } from './constants';
import { PacketTableRow } from './PacketTableRow';
import { PacketTableHeader } from './PacketTableHeader';
import type { CapturedPacket, PacketSortState, PacketSortColumn } from './types';
import './pdump.scss';

const getPacketSortValues = (packet: CapturedPacket) => {
    const { parsed } = packet;

    let source = '';
    let destination = '';
    let protocol = '';

    if (parsed.ipv4) {
        source = parsed.ipv4.srcAddr;
        destination = parsed.ipv4.dstAddr;
        protocol = parsed.ipv4.protocolName;
    } else if (parsed.ipv6) {
        source = parsed.ipv6.srcAddr;
        destination = parsed.ipv6.dstAddr;
        protocol = parsed.ipv6.nextHeaderName;
    } else if (parsed.ethernet) {
        source = parsed.ethernet.srcMac;
        destination = parsed.ethernet.dstMac;
        protocol = parsed.ethernet.etherTypeName;
    }

    if (parsed.tcp) {
        source = `${source}:${parsed.tcp.srcPort}`;
        destination = `${destination}:${parsed.tcp.dstPort}`;
        protocol = 'TCP';
    } else if (parsed.udp) {
        source = `${source}:${parsed.udp.srcPort}`;
        destination = `${destination}:${parsed.udp.dstPort}`;
        protocol = 'UDP';
    } else if (parsed.icmp) {
        protocol = 'ICMP';
    }

    return { source, destination, protocol, length: parsed.raw.length };
};

const createComparator = (column: PacketSortColumn, direction: 'asc' | 'desc') => {
    const mult = direction === 'asc' ? 1 : -1;

    return (a: CapturedPacket, b: CapturedPacket): number => {
        switch (column) {
            case 'index':
                return mult * (a.id - b.id);
            case 'time':
                return mult * (a.timestamp.getTime() - b.timestamp.getTime());
            case 'length':
                return mult * (a.parsed.raw.length - b.parsed.raw.length);
            case 'source': {
                const aVal = getPacketSortValues(a);
                const bVal = getPacketSortValues(b);
                return mult * aVal.source.localeCompare(bVal.source);
            }
            case 'destination': {
                const aVal = getPacketSortValues(a);
                const bVal = getPacketSortValues(b);
                return mult * aVal.destination.localeCompare(bVal.destination);
            }
            case 'protocol': {
                const aVal = getPacketSortValues(a);
                const bVal = getPacketSortValues(b);
                return mult * aVal.protocol.localeCompare(bVal.protocol);
            }
            default:
                return 0;
        }
    };
};

export interface PacketTableProps {
    packets: CapturedPacket[];
    selectedPacketId: number | null;
    onSelectPacket: (packet: CapturedPacket | null) => void;
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

export const PacketTable: React.FC<PacketTableProps> = ({
    packets,
    selectedPacketId,
    onSelectPacket,
    isCapturing,
    configName,
    onClearPackets,
    newPacketIds,
    paused,
    onTogglePause,
    autoScroll,
    onAutoScrollChange,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);
    const containerHeight = useContainerHeight(containerRef);
    const [searchQuery, setSearchQuery] = useState('');
    const [sortState, setSortState] = useState<PacketSortState>({ column: null, direction: 'asc' });

    const handleSort = useCallback((column: PacketSortColumn) => {
        setSortState(prev => ({
            column,
            direction: prev.column === column && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
        onAutoScrollChange(false);
    }, [onAutoScrollChange]);

    const filteredPackets = useMemo(() => {
        if (!searchQuery.trim()) return packets;

        const lowerQuery = searchQuery.toLowerCase();
        return packets.filter(packet => {
            const { parsed } = packet;

            if (parsed.ipv4) {
                if (parsed.ipv4.srcAddr.includes(lowerQuery) || parsed.ipv4.dstAddr.includes(lowerQuery)) {
                    return true;
                }
            }
            if (parsed.ipv6) {
                if (parsed.ipv6.srcAddr.toLowerCase().includes(lowerQuery) ||
                    parsed.ipv6.dstAddr.toLowerCase().includes(lowerQuery)) {
                    return true;
                }
            }

            if (parsed.tcp) {
                if (parsed.tcp.srcPort.toString().includes(lowerQuery) ||
                    parsed.tcp.dstPort.toString().includes(lowerQuery)) {
                    return true;
                }
            }
            if (parsed.udp) {
                if (parsed.udp.srcPort.toString().includes(lowerQuery) ||
                    parsed.udp.dstPort.toString().includes(lowerQuery)) {
                    return true;
                }
            }

            const protocol = parsed.tcp ? 'tcp' : parsed.udp ? 'udp' : parsed.icmp ? 'icmp' : '';
            if (protocol.includes(lowerQuery)) {
                return true;
            }

            if (parsed.ethernet) {
                if (parsed.ethernet.srcMac.toLowerCase().includes(lowerQuery) ||
                    parsed.ethernet.dstMac.toLowerCase().includes(lowerQuery)) {
                    return true;
                }
            }

            return false;
        });
    }, [packets, searchQuery]);

    const sortedPackets = useMemo(() => {
        if (!sortState.column) return filteredPackets;

        const comparator = createComparator(sortState.column, sortState.direction);
        return [...filteredPackets].sort(comparator);
    }, [filteredPackets, sortState]);

    const rowVirtualizer = useVirtualizer({
        count: sortedPackets.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // Use the id of the newest packet rather than sortedPackets.length so this
    // effect re-fires after every flush even when the ring buffer is full and
    // length stays pinned at MAX_PACKETS.
    const lastPacketId = sortedPackets.length > 0 ? sortedPackets[sortedPackets.length - 1]?.id : null;

    useEffect(() => {
        if (autoScroll && !paused && isCapturing && sortedPackets.length > 0 && parentRef.current && !sortState.column) {
            rowVirtualizer.scrollToIndex(sortedPackets.length - 1, { align: 'end' });
        }
    }, [lastPacketId, autoScroll, paused, isCapturing, rowVirtualizer, sortState.column]);

    const STICKY_BOTTOM_THRESHOLD_PX = 24;

    useEffect(() => {
        const element = parentRef.current;
        if (!element || !isCapturing) return;

        const handleScroll = () => {
            const el = parentRef.current;
            if (!el) return;
            const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
            const atBottom = distance < STICKY_BOTTOM_THRESHOLD_PX;
            onAutoScrollChange(atBottom);
        };

        element.addEventListener('scroll', handleScroll, { passive: true });
        return () => element.removeEventListener('scroll', handleScroll);
    }, [isCapturing, onAutoScrollChange]);

    const selectedPacketIdRef = useRef(selectedPacketId);
    selectedPacketIdRef.current = selectedPacketId;

    const handleSelectPacket = useCallback((packet: CapturedPacket) => {
        onSelectPacket(packet.id === selectedPacketIdRef.current ? null : packet);
    }, [onSelectPacket]);

    const handleSearchChange = useCallback((value: string) => {
        setSearchQuery(value);
    }, []);

    const handleToggleAutoScroll = useCallback(() => {
        onAutoScrollChange(!autoScroll);
    }, [onAutoScrollChange, autoScroll]);

    const statsText = useMemo(() => {
        const total = packets.length;
        const filtered = sortedPackets.length;
        if (searchQuery.trim() && filtered !== total) {
            return `${filtered.toLocaleString()} / ${total.toLocaleString()} packets`;
        }
        return `${total.toLocaleString()} packets`;
    }, [packets.length, sortedPackets.length, searchQuery]);

    if (containerHeight === 0) {
        return <div ref={containerRef} className="packet-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} – ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${sortedPackets.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="packet-table" style={{ height: containerHeight }}>
            <div className="packet-table__toolbar" style={{ height: SEARCH_BAR_HEIGHT }}>
                <div className="packet-table__search">
                    <TextInput
                        placeholder="Filter by IP, port, protocol..."
                        value={searchQuery}
                        onUpdate={handleSearchChange}
                        size="m"
                        hasClear
                    />
                </div>
                <div className="packet-table__toolbar-info">
                    <span className="packet-table__stats">{statsText}</span>
                    {configName && isCapturing && (
                        <span className={`packet-table__live-badge${paused ? ' packet-table__live-badge--paused' : ''}`}>
                            {paused ? 'PAUSED' : 'LIVE'}
                        </span>
                    )}
                </div>
                <div className="packet-table__toolbar-actions">
                    {isCapturing && (
                        <button
                            type="button"
                            className={`fw-btn fw-btn--ghost fw-btn--sm${autoScroll ? ' packet-table__btn--active' : ''}`}
                            onClick={handleToggleAutoScroll}
                            title="Toggle auto-scroll to new packets"
                        >
                            Auto-scroll
                        </button>
                    )}
                    <button
                        type="button"
                        className={`fw-btn fw-btn--ghost fw-btn--sm${paused ? ' packet-table__btn--active' : ''}`}
                        onClick={onTogglePause}
                        title="Pause / resume view updates"
                    >
                        {paused ? 'Resume' : 'Pause'}
                    </button>
                    <button
                        type="button"
                        className="fw-btn fw-btn--ghost fw-btn--sm"
                        onClick={onClearPackets}
                        disabled={packets.length === 0}
                        title="Clear all captured packets"
                    >
                        Clear
                    </button>
                </div>
            </div>

            <div className="packet-table__wrapper">
                <PacketTableHeader
                    sortState={sortState}
                    onSort={handleSort}
                />

                <div
                    ref={parentRef}
                    className="packet-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {sortedPackets.length === 0 ? (
                        <div className="packet-table__empty">
                            <EmptyState message={
                                !configName
                                    ? 'Select a config and start capture to see packets.'
                                    : packets.length === 0
                                        ? 'Waiting for packets matching the filter...'
                                        : 'No packets match the filter'
                            } />
                        </div>
                    ) : (
                        <div
                            className="packet-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                const packet = sortedPackets[virtualRow.index];
                                if (!packet) return null;

                                const isSelected = packet.id === selectedPacketId;
                                const isNew = newPacketIds.has(packet.id) && !paused;

                                return (
                                    <PacketTableRow
                                        key={packet.id}
                                        packet={packet}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        isNew={isNew}
                                        onSelect={handleSelectPacket}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>

            <div className="packet-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="packet-table__footer-text">{footerText}</span>
                <span className="packet-table__footer-text">
                    {sortState.column ? `Sorted by ${sortState.column} · ` : ''}Click to inspect
                </span>
            </div>
        </div>
    );
};
