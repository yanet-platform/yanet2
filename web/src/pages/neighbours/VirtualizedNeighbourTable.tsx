import React, { useRef, useCallback, useMemo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text, TextInput, Label } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { Neighbour } from '../../api/neighbours';
import type { SortState, SortableColumn } from './hooks';
import { useContainerHeight, useProcessedNeighbours } from './hooks';
import { ROW_HEIGHT, OVERSCAN, TOTAL_WIDTH, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT } from './constants';
import { NeighbourVirtualRow } from './NeighbourVirtualRow';
import { NeighbourTableHeader } from './NeighbourTableHeader';

export interface VirtualizedNeighbourTableProps {
    neighbours: Neighbour[];
    selectedIds: Set<string>;
    onSelectionChange: (ids: string[]) => void;
    onEditNeighbour?: (neighbour: Neighbour) => void;
    sortState: SortState;
    onSort: (column: SortableColumn) => void;
    searchQuery: string;
    onSearchChange: (query: string) => void;
}

const getNeighbourId = (n: Neighbour): string => n.next_hop || '';

export const VirtualizedNeighbourTable: React.FC<VirtualizedNeighbourTableProps> = ({
    neighbours,
    selectedIds,
    onSelectionChange,
    onEditNeighbour,
    sortState,
    onSort,
    searchQuery,
    onSearchChange,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    const containerHeight = useContainerHeight(containerRef);

    const processedData = useProcessedNeighbours(neighbours, searchQuery, sortState);

    // Virtualizer
    const rowVirtualizer = useVirtualizer({
        count: processedData.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // Row select handler
    const handleRowSelect = useCallback((neighbour: Neighbour, checked: boolean) => {
        const id = getNeighbourId(neighbour);
        const newSelection = new Set(selectedIds);
        if (checked) {
            newSelection.add(id);
        } else {
            newSelection.delete(id);
        }
        onSelectionChange(Array.from(newSelection));
    }, [selectedIds, onSelectionChange]);

    const handleClearSelection = useCallback(() => {
        onSelectionChange([]);
    }, [onSelectionChange]);

    // Stats text
    const isFiltered = !!searchQuery.trim();
    const statsText = useMemo(() => {
        if (isFiltered) {
            return `Found: ${processedData.length.toLocaleString()} of ${neighbours.length.toLocaleString()}`;
        }
        return `Total: ${processedData.length.toLocaleString()}`;
    }, [isFiltered, processedData.length, neighbours.length]);

    const selectedText = useMemo(() => {
        return selectedIds.size > 0 ? `Selected: ${selectedIds.size.toLocaleString()}` : null;
    }, [selectedIds.size]);

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} className="neigh-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    // Footer text
    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${processedData.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="neigh-table" style={{ height: containerHeight }}>
            {/* Search bar */}
            <Box className="neigh-search-bar" style={{ height: SEARCH_BAR_HEIGHT }}>
                <Box className="neigh-search-bar__input">
                    <TextInput
                        placeholder="Search by next hop, device, or source..."
                        value={searchQuery}
                        onUpdate={onSearchChange}
                        size="m"
                        hasClear
                    />
                </Box>
                <Box className="neigh-search-bar__stats">
                    <Label theme="info" size="m">{statsText}</Label>
                    {selectedText && (
                        <>
                            <Label theme="warning" size="m">{selectedText}</Label>
                            <Text
                                variant="body-1"
                                color="link"
                                className="neigh-search-bar__clear"
                                onClick={handleClearSelection}
                            >
                                Clear
                            </Text>
                        </>
                    )}
                </Box>
            </Box>

            {/* Table container */}
            <Box className="neigh-table__wrapper">
                <NeighbourTableHeader
                    sortState={sortState}
                    onSort={onSort}
                />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="neigh-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {processedData.length === 0 ? (
                        <Box className="neigh-table__empty">
                            <EmptyState message="No neighbours found" />
                        </Box>
                    ) : (
                        <div
                            className="neigh-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                const neighbour = processedData[virtualRow.index];
                                if (!neighbour) return null;

                                const id = getNeighbourId(neighbour);
                                const isSelected = selectedIds.has(id);

                                return (
                                    <NeighbourVirtualRow
                                        key={virtualRow.index}
                                        neighbour={neighbour}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        onSelect={handleRowSelect}
                                        onEdit={onEditNeighbour}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            {/* Footer */}
            <Box className="neigh-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
