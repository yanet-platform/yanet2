import React, { useRef, useCallback, useMemo, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text } from '@gravity-ui/uikit';
import { EmptyState, TableSearchBar, SortableTableHeader } from '../../components';
import type { FIBEntry, FIBNexthop } from '../../api/routes';
import type { FIBRow, FIBSortableColumn, FIBSortState } from './types';
import { useContainerHeight } from './hooks';
import { ROW_HEIGHT, OVERSCAN, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT, FIB_TOTAL_WIDTH, fibCellStyles } from './constants';
import './route.scss';

export interface FIBTableProps {
    entries: FIBEntry[];
}

const flattenEntries = (entries: FIBEntry[]): FIBRow[] => {
    const rows: FIBRow[] = [];
    for (const entry of entries) {
        const prefix = entry.prefix || '';
        const nexthops = entry.nexthops || [];
        if (nexthops.length === 0) {
            rows.push({
                id: `${prefix}-no-nh`,
                prefix,
                dst_mac: '',
                src_mac: '',
                device: '',
            });
        } else {
            nexthops.forEach((nh: FIBNexthop, idx: number) => {
                rows.push({
                    id: `${prefix}-${idx}`,
                    prefix,
                    dst_mac: nh.dst_mac || '',
                    src_mac: nh.src_mac || '',
                    device: nh.device || '',
                });
            });
        }
    }
    return rows;
};

const fibSortComparators: Record<FIBSortableColumn, (a: FIBRow, b: FIBRow) => number> = {
    prefix: (a, b) => a.prefix.localeCompare(b.prefix),
    dst_mac: (a, b) => a.dst_mac.localeCompare(b.dst_mac),
    src_mac: (a, b) => a.src_mac.localeCompare(b.src_mac),
    device: (a, b) => a.device.localeCompare(b.device),
};


const FIBTableHeader: React.FC<{
    sortState: FIBSortState;
    onSort: (column: FIBSortableColumn) => void;
}> = ({ sortState, onSort }) => {
    return (
        <Box
            className="route-table-header-box"
            style={{ height: HEADER_HEIGHT, minWidth: FIB_TOTAL_WIDTH }}
        >
            <Box style={{ ...fibCellStyles.index, color: undefined }}>
                <Text variant="subheader-1">#</Text>
            </Box>
            <SortableTableHeader column="prefix" label="Prefix" style={fibCellStyles.prefix} sortState={sortState} onSort={onSort} />
            <SortableTableHeader column="dst_mac" label="Dst MAC" style={fibCellStyles.dst_mac} sortState={sortState} onSort={onSort} />
            <SortableTableHeader column="src_mac" label="Src MAC" style={fibCellStyles.src_mac} sortState={sortState} onSort={onSort} />
            <SortableTableHeader column="device" label="Device" style={fibCellStyles.device} sortState={sortState} onSort={onSort} />
        </Box>
    );
};

const FIBVirtualRow: React.FC<{
    row: FIBRow;
    index: number;
    start: number;
}> = ({ row, index, start }) => {
    return (
        <div
            style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                minWidth: FIB_TOTAL_WIDTH,
                height: ROW_HEIGHT,
                transform: `translateY(${start}px)`,
                display: 'flex',
                alignItems: 'center',
                padding: '0 8px',
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: index % 2 === 0
                    ? 'transparent'
                    : 'var(--g-color-base-generic-ultralight)',
                boxSizing: 'border-box',
            }}
        >
            <div style={fibCellStyles.index}>{index + 1}</div>
            <div style={fibCellStyles.prefix}>{row.prefix || '-'}</div>
            <div style={fibCellStyles.dst_mac}>{row.dst_mac || '-'}</div>
            <div style={fibCellStyles.src_mac}>{row.src_mac || '-'}</div>
            <div style={fibCellStyles.device}>{row.device || '-'}</div>
        </div>
    );
};

export const FIBTable: React.FC<FIBTableProps> = ({ entries }) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    const [searchQuery, setSearchQuery] = useState('');
    const [sortState, setSortState] = useState<FIBSortState>({ column: null, direction: 'asc' });

    const containerHeight = useContainerHeight(containerRef);

    const allRows = useMemo(() => flattenEntries(entries), [entries]);

    const handleSort = useCallback((column: FIBSortableColumn) => {
        setSortState(prev => ({
            column,
            direction: prev.column === column && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
    }, []);

    const processedRows = useMemo(() => {
        let result = allRows;

        if (searchQuery.trim()) {
            const lower = searchQuery.toLowerCase();
            result = result.filter(row =>
                row.prefix.toLowerCase().includes(lower) ||
                row.dst_mac.toLowerCase().includes(lower) ||
                row.src_mac.toLowerCase().includes(lower) ||
                row.device.toLowerCase().includes(lower),
            );
        }

        if (sortState.column) {
            const comparator = fibSortComparators[sortState.column];
            result = [...result].sort(comparator);
            if (sortState.direction === 'desc') {
                result.reverse();
            }
        }

        return result;
    }, [allRows, searchQuery, sortState]);

    const rowVirtualizer = useVirtualizer({
        count: processedRows.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    const statsText = useMemo(() => {
        if (searchQuery.trim()) {
            return `Found: ${processedRows.length.toLocaleString()} of ${allRows.length.toLocaleString()}`;
        }
        return `Total: ${processedRows.length.toLocaleString()}`;
    }, [searchQuery, processedRows.length, allRows.length]);

    if (containerHeight === 0) {
        return <div ref={containerRef} className="route-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${processedRows.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="route-table" style={{ height: containerHeight }}>
            <TableSearchBar
                searchQuery={searchQuery}
                onSearchChange={setSearchQuery}
                isSearching={false}
                statsText={statsText}
                selectedText={null}
                placeholder="Search by prefix, MAC, or device..."
                height={SEARCH_BAR_HEIGHT}
                inputWidth={350}
            />

            <Box className="route-table__wrapper">
                <FIBTableHeader
                    sortState={sortState}
                    onSort={handleSort}
                />

                <div
                    ref={parentRef}
                    className="route-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {processedRows.length === 0 ? (
                        <Box className="route-table__empty">
                            <EmptyState message="No FIB entries found. Flush RIB to FIB first." />
                        </Box>
                    ) : (
                        <div
                            className="route-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: FIB_TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                const row = processedRows[virtualRow.index];
                                if (!row) return null;

                                return (
                                    <FIBVirtualRow
                                        key={virtualRow.index}
                                        row={row}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            <Box className="route-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
