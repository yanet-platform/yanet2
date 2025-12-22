import React, { useRef, useCallback, useMemo, useState, useLayoutEffect } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { PrefixItem } from './types';
import { ROW_HEIGHT, OVERSCAN, TOTAL_WIDTH, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT } from './constants';
import { PrefixRow } from './PrefixRow';
import { PrefixSearchBar } from './PrefixSearchBar';
import { PrefixTableHeader } from './PrefixTableHeader';
import './decap.css';

export interface PrefixTableProps {
    prefixes: PrefixItem[];
    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
}

export const PrefixTable: React.FC<PrefixTableProps> = ({
    prefixes,
    selectedIds,
    onSelectionChange,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    const [searchQuery, setSearchQuery] = useState('');
    const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc');
    const [containerHeight, setContainerHeight] = useState(0);

    // Measure available height from window
    useLayoutEffect(() => {
        const updateHeight = () => {
            if (containerRef.current) {
                const rect = containerRef.current.getBoundingClientRect();
                const availableHeight = window.innerHeight - rect.top - 20;
                setContainerHeight(Math.max(300, availableHeight));
            }
        };

        updateHeight();
        window.addEventListener('resize', updateHeight);

        return () => {
            window.removeEventListener('resize', updateHeight);
        };
    }, []);

    // Filter and sort data
    const processedData = useMemo(() => {
        let result = prefixes;

        // Apply search filter
        if (searchQuery.trim()) {
            const lowerQuery = searchQuery.toLowerCase();
            result = result.filter(prefix =>
                prefix.prefix.toLowerCase().includes(lowerQuery)
            );
        }

        // Apply sorting
        result = [...result].sort((a, b) => {
            const cmp = a.prefix.localeCompare(b.prefix);
            return sortDirection === 'asc' ? cmp : -cmp;
        });

        return result;
    }, [prefixes, searchQuery, sortDirection]);

    const handleSort = useCallback(() => {
        setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc');
    }, []);

    const handleSearchChange = useCallback((value: string) => {
        setSearchQuery(value);
    }, []);

    // Virtualizer
    const rowVirtualizer = useVirtualizer({
        count: processedData.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    const handleRowSelect = useCallback((prefix: PrefixItem, checked: boolean) => {
        const newSelection = new Set(selectedIds);
        if (checked) {
            newSelection.add(prefix.id);
        } else {
            newSelection.delete(prefix.id);
        }
        onSelectionChange(newSelection);
    }, [selectedIds, onSelectionChange]);

    const handleClearSelection = useCallback(() => {
        onSelectionChange(new Set());
    }, [onSelectionChange]);

    const handleSelectAll = useCallback(() => {
        if (selectedIds.size === processedData.length && processedData.length > 0) {
            // Deselect all
            onSelectionChange(new Set());
        } else {
            // Select all visible (filtered) items
            onSelectionChange(new Set(processedData.map(p => p.id)));
        }
    }, [selectedIds.size, processedData, onSelectionChange]);

    const isAllSelected = processedData.length > 0 && selectedIds.size === processedData.length;
    const isIndeterminate = selectedIds.size > 0 && selectedIds.size < processedData.length;

    // Stats
    const statsText = useMemo(() => {
        if (searchQuery.trim()) {
            return `Found: ${processedData.length.toLocaleString()} of ${prefixes.length.toLocaleString()}`;
        }
        return `Total: ${prefixes.length.toLocaleString()}`;
    }, [searchQuery, processedData.length, prefixes.length]);

    const selectedText = useMemo(() => {
        return selectedIds.size > 0 ? `Selected: ${selectedIds.size.toLocaleString()}` : null;
    }, [selectedIds.size]);

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} className="prefix-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    // Footer text
    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${processedData.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="prefix-table" style={{ height: containerHeight }}>
            <PrefixSearchBar
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                isSearching={false}
                statsText={statsText}
                selectedText={selectedText}
                onClearSelection={handleClearSelection}
            />

            {/* Table container */}
            <Box className="prefix-table__wrapper">
                <PrefixTableHeader
                    sortDirection={sortDirection}
                    onSort={handleSort}
                    isAllSelected={isAllSelected}
                    isIndeterminate={isIndeterminate}
                    onSelectAll={handleSelectAll}
                    hasItems={processedData.length > 0}
                />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="prefix-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {processedData.length === 0 ? (
                        <Box className="prefix-table__empty">
                            <EmptyState message="No prefixes found" />
                        </Box>
                    ) : (
                        <div
                            className="prefix-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                const prefix = processedData[virtualRow.index];
                                if (!prefix) return null;

                                const isSelected = selectedIds.has(prefix.id);

                                return (
                                    <PrefixRow
                                        key={prefix.id}
                                        prefix={prefix}
                                        index={virtualRow.index}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        onSelect={handleRowSelect}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            {/* Footer */}
            <Box className="prefix-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
