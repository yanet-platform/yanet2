import React, { useRef, useMemo, useState, useCallback, useEffect } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import { ROW_HEIGHT, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT, OVERSCAN, TOTAL_WIDTH } from './constants';
import { PacketTableRow } from './PacketTableRow';
import { PacketTableHeader } from './PacketTableHeader';
import { PacketSearchBar } from './PacketSearchBar';
import type { CapturedPacket, PacketSortState, PacketSortColumn } from './types';
import './pdump.css';

// Helper to extract sortable values from packet
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

// Sort comparators
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
    onSelectPacket: (id: number | null) => void;
    isCapturing: boolean;
    configName: string | null;
    onStopCapture: () => void;
    onClearPackets: () => void;
}

export const PacketTable: React.FC<PacketTableProps> = ({
    packets,
    selectedPacketId,
    onSelectPacket,
    isCapturing,
    configName,
    onStopCapture,
    onClearPackets,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);
    const [containerHeight, setContainerHeight] = useState(0);
    const [searchQuery, setSearchQuery] = useState('');
    const [autoScroll, setAutoScroll] = useState(true);
    const [sortState, setSortState] = useState<PacketSortState>({ column: null, direction: 'asc' });

    // Measure container height
    useEffect(() => {
        if (!containerRef.current) return;
        const resizeObserver = new ResizeObserver((entries) => {
            const entry = entries[0];
            if (entry) {
                setContainerHeight(entry.contentRect.height);
            }
        });
        resizeObserver.observe(containerRef.current);
        return () => resizeObserver.disconnect();
    }, []);

    // Handle sort
    const handleSort = useCallback((column: PacketSortColumn) => {
        setSortState(prev => ({
            column,
            direction: prev.column === column && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
        // Disable auto-scroll when sorting
        setAutoScroll(false);
    }, []);

    // Filter packets based on search query
    const filteredPackets = useMemo(() => {
        if (!searchQuery.trim()) return packets;

        const lowerQuery = searchQuery.toLowerCase();
        return packets.filter(packet => {
            const { parsed } = packet;

            // Search in IP addresses
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

            // Search in ports
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

            // Search in protocol
            const protocol = parsed.tcp ? 'tcp' : parsed.udp ? 'udp' : parsed.icmp ? 'icmp' : '';
            if (protocol.includes(lowerQuery)) {
                return true;
            }

            // Search in MAC addresses
            if (parsed.ethernet) {
                if (parsed.ethernet.srcMac.toLowerCase().includes(lowerQuery) ||
                    parsed.ethernet.dstMac.toLowerCase().includes(lowerQuery)) {
                    return true;
                }
            }

            return false;
        });
    }, [packets, searchQuery]);

    // Sort filtered packets
    const sortedPackets = useMemo(() => {
        if (!sortState.column) return filteredPackets;

        const comparator = createComparator(sortState.column, sortState.direction);
        return [...filteredPackets].sort(comparator);
    }, [filteredPackets, sortState]);

    // Virtualizer
    const rowVirtualizer = useVirtualizer({
        count: sortedPackets.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // Auto-scroll to bottom when new packets arrive
    useEffect(() => {
        if (autoScroll && isCapturing && sortedPackets.length > 0 && parentRef.current && !sortState.column) {
            rowVirtualizer.scrollToIndex(sortedPackets.length - 1, { align: 'end' });
        }
    }, [sortedPackets.length, autoScroll, isCapturing, rowVirtualizer, sortState.column]);

    // Detect mouse wheel scroll to disable auto-scroll
    // Using wheel event instead of scroll event to avoid race conditions with programmatic scrolling
    useEffect(() => {
        const element = parentRef.current;
        if (!element || !isCapturing) return;

        const handleWheel = (e: WheelEvent) => {
            // Only disable auto-scroll when user scrolls up
            if (autoScroll && e.deltaY < 0) {
                setAutoScroll(false);
            }
        };

        element.addEventListener('wheel', handleWheel, { passive: true });
        return () => element.removeEventListener('wheel', handleWheel);
    }, [isCapturing, autoScroll]);

    const handleSelectPacket = useCallback((id: number) => {
        onSelectPacket(id === selectedPacketId ? null : id);
    }, [onSelectPacket, selectedPacketId]);

    const handleSearchChange = useCallback((value: string) => {
        setSearchQuery(value);
    }, []);

    const handleToggleAutoScroll = useCallback(() => {
        setAutoScroll(prev => !prev);
    }, []);

    // Stats text
    const statsText = useMemo(() => {
        const total = packets.length;
        const filtered = sortedPackets.length;
        if (searchQuery.trim() && filtered !== total) {
            return `Showing ${filtered.toLocaleString()} of ${total.toLocaleString()} packets`;
        }
        return `${total.toLocaleString()} packets`;
    }, [packets.length, sortedPackets.length, searchQuery]);

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} className="packet-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    // Footer text
    const footerText = virtualRows.length > 0
        ? `Packets ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${sortedPackets.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="packet-table" style={{ height: containerHeight }}>
            <PacketSearchBar
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                statsText={statsText}
                isCapturing={isCapturing}
                configName={configName}
                onStopCapture={onStopCapture}
                onClearPackets={onClearPackets}
                canClear={packets.length > 0}
                autoScroll={autoScroll}
                onToggleAutoScroll={handleToggleAutoScroll}
            />

            {/* Table container */}
            <Box className="packet-table__wrapper">
                <PacketTableHeader
                    sortState={sortState}
                    onSort={handleSort}
                />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="packet-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {sortedPackets.length === 0 ? (
                        <Box className="packet-table__empty">
                            <EmptyState message={packets.length === 0 ? 'No packets captured yet' : 'No packets match the filter'} />
                        </Box>
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

                                return (
                                    <PacketTableRow
                                        key={packet.id}
                                        packet={packet}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        onClick={() => handleSelectPacket(packet.id)}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            {/* Footer */}
            <Box className="packet-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">
                    {sortState.column ? `Sorted by ${sortState.column} • ` : ''}
                    Click to inspect
                </Text>
            </Box>
        </div>
    );
};
