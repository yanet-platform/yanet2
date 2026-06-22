import React, { useRef, useMemo, useCallback, useEffect, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { TextInput } from '@gravity-ui/uikit';
import { EmptyState } from '../EmptyState';
import { useContainerHeight } from '../../hooks';
import { PKT_ROW_HEIGHT, PKT_SEARCH_BAR_HEIGHT, PKT_HEADER_HEIGHT, PKT_FOOTER_HEIGHT, PKT_OVERSCAN, PKT_TOTAL_WIDTH } from './constants';
import { SharedPacketTableRow } from './PacketTableRow';
import { SharedPacketTableHeader } from './PacketTableHeader';
import type { PacketRow, PacketSortState, PacketSortColumn } from './types';
import './packet.scss';

const getPacketSortValues = (packet: PacketRow) => {
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

    return (a: PacketRow, b: PacketRow): number => {
        switch (column) {
            case 'index':
                return mult * (a.id - b.id);
            case 'time': {
                const at = a.timestamp?.getTime() ?? 0;
                const bt = b.timestamp?.getTime() ?? 0;
                return mult * (at - bt);
            }
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
    /** Rows to display. Each item must have a unique numeric id. */
    packets: PacketRow[];
    selectedPacketId: number | null;
    onSelectPacket: (packet: PacketRow | null) => void;
    searchQuery: string;
    onSearchQueryChange: (value: string) => void;
    /** IDs of packets that should flash the "new" animation. */
    newPacketIds?: Set<number>;
    /** Whether the view is paused (hides LIVE badge, disables auto-scroll). */
    paused?: boolean;
    onTogglePause?: () => void;
    autoScroll?: boolean;
    onAutoScrollChange?: (value: boolean) => void;
    onClearPackets?: () => void;
    /** If truthy and isLive is true, shows the LIVE/PAUSED badge next to the packet count. */
    isLive?: boolean;
    /** Message to show when there are no packets at all. */
    emptyMessage?: string;
    /** Message to show when search filters out everything. */
    emptyFilterMessage?: string;
    /** If false, no auto-scroll or clear toolbar actions are rendered. Default true. */
    showToolbarActions?: boolean;
    /** Whether to show the Time column. Defaults to true. */
    showTimeColumn?: boolean;
    /** Optional note to show in the footer right slot (e.g. "truncated" warning). */
    footerNote?: React.ReactNode;
}

/**
 * Generic virtualized packet table shared across packet-display surfaces.
 *
 * Handles filtering, sorting, virtualisation, auto-scroll, and selection.
 * Rendering of capture-specific metadata is the caller's responsibility.
 */
export const PacketTable: React.FC<PacketTableProps> = ({
    packets,
    selectedPacketId,
    onSelectPacket,
    searchQuery,
    onSearchQueryChange,
    newPacketIds = new Set(),
    paused = false,
    onTogglePause,
    autoScroll = false,
    onAutoScrollChange,
    onClearPackets,
    isLive = false,
    emptyMessage = 'No packets to display.',
    emptyFilterMessage = 'No packets match the filter.',
    showToolbarActions = true,
    showTimeColumn = true,
    footerNote,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);
    const containerHeight = useContainerHeight(containerRef);
    const [sortState, setSortState] = useState<PacketSortState>({ column: null, direction: 'asc' });

    const handleSort = useCallback((column: PacketSortColumn) => {
        setSortState(prev => ({
            column,
            direction: prev.column === column && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
        onAutoScrollChange?.(false);
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
        estimateSize: () => PKT_ROW_HEIGHT,
        overscan: PKT_OVERSCAN,
    });

    const lastPacketId = sortedPackets.length > 0 ? sortedPackets[sortedPackets.length - 1]?.id : null;

    useEffect(() => {
        if (autoScroll && !paused && isLive && sortedPackets.length > 0 && parentRef.current && !sortState.column) {
            rowVirtualizer.scrollToIndex(sortedPackets.length - 1, { align: 'end' });
        }
    }, [lastPacketId, autoScroll, paused, isLive, rowVirtualizer, sortState.column]);

    const STICKY_BOTTOM_THRESHOLD_PX = 24;

    useEffect(() => {
        const element = parentRef.current;
        if (!element || !isLive) return;

        const handleScroll = (): void => {
            const el = parentRef.current;
            if (!el) return;
            const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
            onAutoScrollChange?.(distance < STICKY_BOTTOM_THRESHOLD_PX);
        };

        element.addEventListener('scroll', handleScroll, { passive: true });
        return () => element.removeEventListener('scroll', handleScroll);
    }, [isLive, onAutoScrollChange]);

    const selectedPacketIdRef = useRef(selectedPacketId);
    selectedPacketIdRef.current = selectedPacketId;

    const handleSelectPacket = useCallback((packet: PacketRow) => {
        onSelectPacket(packet.id === selectedPacketIdRef.current ? null : packet);
    }, [onSelectPacket]);

    const handleSearchChange = useCallback((value: string) => {
        onSearchQueryChange(value);
    }, [onSearchQueryChange]);

    const handleToggleAutoScroll = useCallback(() => {
        onAutoScrollChange?.(!autoScroll);
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

    const tableBodyHeight = containerHeight - PKT_SEARCH_BAR_HEIGHT - PKT_HEADER_HEIGHT - PKT_FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerLeft = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} – ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${sortedPackets.length.toLocaleString()}`
        : '';

    const footerRight = footerNote ?? (
        sortState.column ? `Sorted by ${sortState.column} · Click to inspect` : 'Click to inspect'
    );

    return (
        <div ref={containerRef} className="packet-table" style={{ height: containerHeight }}>
            <div className="packet-table__toolbar" style={{ height: PKT_SEARCH_BAR_HEIGHT }}>
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
                    {isLive && (
                        <span className={`packet-table__live-badge${paused ? ' packet-table__live-badge--paused' : ''}`}>
                            {paused ? 'PAUSED' : 'LIVE'}
                        </span>
                    )}
                </div>
                {showToolbarActions && (
                    <div className="packet-table__toolbar-actions">
                        {isLive && onAutoScrollChange && (
                            <button
                                type="button"
                                className={`yn-btn yn-btn--ghost yn-btn--sm${autoScroll ? ' packet-table__btn--active' : ''}`}
                                onClick={handleToggleAutoScroll}
                                title="Toggle auto-scroll to new packets"
                            >
                                Auto-scroll
                            </button>
                        )}
                        {onTogglePause && (
                            <button
                                type="button"
                                className={`yn-btn yn-btn--ghost yn-btn--sm${paused ? ' packet-table__btn--active' : ''}`}
                                onClick={onTogglePause}
                                title="Pause / resume view updates"
                            >
                                {paused ? 'Resume' : 'Pause'}
                            </button>
                        )}
                        {onClearPackets && (
                            <button
                                type="button"
                                className="yn-btn yn-btn--ghost yn-btn--sm"
                                onClick={onClearPackets}
                                disabled={packets.length === 0}
                                title="Clear all packets"
                            >
                                Clear
                            </button>
                        )}
                    </div>
                )}
            </div>

            <div className="packet-table__wrapper">
                <SharedPacketTableHeader
                    sortState={sortState}
                    onSort={handleSort}
                    showTime={showTimeColumn}
                />

                <div
                    ref={parentRef}
                    className="packet-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {sortedPackets.length === 0 ? (
                        <div className="packet-table__empty">
                            <EmptyState message={
                                packets.length === 0 ? emptyMessage : emptyFilterMessage
                            } />
                        </div>
                    ) : (
                        <div
                            className="packet-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: PKT_TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                const packet = sortedPackets[virtualRow.index];
                                if (!packet) return null;

                                const isSelected = packet.id === selectedPacketId;
                                const isNew = newPacketIds.has(packet.id) && !paused;

                                return (
                                    <SharedPacketTableRow
                                        key={packet.id}
                                        packet={packet}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        isNew={isNew}
                                        onSelect={handleSelectPacket}
                                        showTime={showTimeColumn}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>

            <div className="packet-table__footer" style={{ height: PKT_FOOTER_HEIGHT }}>
                <span className="packet-table__footer-text">{footerLeft}</span>
                <span className="packet-table__footer-text">{footerRight}</span>
            </div>
        </div>
    );
};
