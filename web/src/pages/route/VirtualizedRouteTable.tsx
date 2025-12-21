import React, { useRef, useCallback, useMemo, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { Route } from '../../api/routes';
import type { MockRouteGenerator } from './mockData';
import { useSortState, useContainerHeight, sortComparators } from './hooks';
import { ROW_HEIGHT, OVERSCAN, TOTAL_WIDTH, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT } from './constants';
import { VirtualRow } from './VirtualRow';
import { TableSearchBar } from './TableSearchBar';
import { TableHeader } from './TableHeader';
import './route.css';

// Data source can be either a generator (for massive datasets) or a direct array
type DataSource =
    | { type: 'generator'; generator: MockRouteGenerator }
    | { type: 'array'; routes: Route[] };

export interface VirtualizedRouteTableProps {
    // Generator mode (for mocks with massive datasets)
    generator?: MockRouteGenerator;
    // Array mode (for real data)
    routes?: Route[];
    // Common props
    selectedIds: Set<string>;
    onSelectionChange: (ids: string[]) => void;
    getRouteId: (route: Route) => string;
}

export const VirtualizedRouteTable: React.FC<VirtualizedRouteTableProps> = ({
    generator,
    routes,
    selectedIds,
    onSelectionChange,
    getRouteId,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    // Local search state
    const [searchQuery, setSearchQuery] = useState('');
    const [isSearching, setIsSearching] = useState(false);

    const containerHeight = useContainerHeight(containerRef);
    const { sortState, handleSort } = useSortState();

    // Determine data source
    const dataSource: DataSource = useMemo(() => {
        if (generator) {
            return { type: 'generator', generator };
        }
        return { type: 'array', routes: routes || [] };
    }, [generator, routes]);

    // For array mode: filter and sort data locally
    const processedArrayData = useMemo(() => {
        if (dataSource.type !== 'array') return null;

        let result = dataSource.routes;

        // Apply search filter
        if (searchQuery.trim()) {
            const lowerQuery = searchQuery.toLowerCase();
            result = result.filter(route =>
                route.prefix?.toLowerCase().includes(lowerQuery) ||
                route.nextHop?.toLowerCase().includes(lowerQuery) ||
                route.peer?.toLowerCase().includes(lowerQuery)
            );
        }

        // Apply sorting
        if (sortState.column) {
            const comparator = sortComparators[sortState.column];
            result = [...result].sort(comparator);
            if (sortState.direction === 'desc') {
                result.reverse();
            }
        }

        return result;
    }, [dataSource, searchQuery, sortState]);

    // For generator mode: search using generator's searchRoutes
    const [generatorSearchResults, setGeneratorSearchResults] = useState<{ routes: Route[]; totalMatched: number } | null>(null);

    const handleSearchChange = useCallback((value: string) => {
        setSearchQuery(value);

        if (dataSource.type === 'generator') {
            if (!value.trim()) {
                setGeneratorSearchResults(null);
                setIsSearching(false);
                return;
            }

            setIsSearching(true);
            // Debounce search for generator mode
            const timeoutId = setTimeout(() => {
                const results = dataSource.generator.searchRoutes(value, 0, 10000);
                setGeneratorSearchResults(results);
                setIsSearching(false);
            }, 300);

            return () => clearTimeout(timeoutId);
        }
    }, [dataSource]);

    // Sorted generator search results
    const sortedGeneratorResults = useMemo(() => {
        if (dataSource.type !== 'generator' || !generatorSearchResults) return null;
        if (!sortState.column) return generatorSearchResults.routes;

        const comparator = sortComparators[sortState.column];
        const sorted = [...generatorSearchResults.routes].sort(comparator);
        return sortState.direction === 'desc' ? sorted.reverse() : sorted;
    }, [dataSource.type, generatorSearchResults, sortState]);

    // Determine display data and count
    const { displayData, displayCount, isFiltered, totalCount } = useMemo(() => {
        if (dataSource.type === 'array') {
            const total = dataSource.routes.length;
            const data = processedArrayData || [];
            return {
                displayData: data,
                displayCount: data.length,
                isFiltered: !!searchQuery.trim(),
                totalCount: total,
            };
        } else {
            // Generator mode
            const isFilteredGen = !!searchQuery.trim() && generatorSearchResults !== null;
            if (isFilteredGen) {
                return {
                    displayData: sortedGeneratorResults || [],
                    displayCount: sortedGeneratorResults?.length || 0,
                    isFiltered: true,
                    totalCount: dataSource.generator.totalCount,
                };
            }
            return {
                displayData: null, // Will use generator.getRoute()
                displayCount: dataSource.generator.totalCount,
                isFiltered: false,
                totalCount: dataSource.generator.totalCount,
            };
        }
    }, [dataSource, processedArrayData, searchQuery, generatorSearchResults, sortedGeneratorResults]);

    // Can sort when not using generator or when filtered (for generator mode)
    const canSort = dataSource.type === 'array' || isFiltered;

    // Virtualizer
    const rowVirtualizer = useVirtualizer({
        count: displayCount,
        getScrollElement: () => parentRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    // Stable row select handler
    const handleRowSelect = useCallback((route: Route, checked: boolean) => {
        const id = getRouteId(route);
        const newSelection = new Set(selectedIds);
        if (checked) {
            newSelection.add(id);
        } else {
            newSelection.delete(id);
        }
        onSelectionChange(Array.from(newSelection));
    }, [selectedIds, onSelectionChange, getRouteId]);

    const handleClearSelection = useCallback(() => {
        onSelectionChange([]);
    }, [onSelectionChange]);

    // Memoized stats text
    const statsText = useMemo(() => {
        if (isFiltered) {
            if (dataSource.type === 'generator' && generatorSearchResults) {
                return `Found: ${generatorSearchResults.totalMatched.toLocaleString()}`;
            }
            return `Found: ${displayCount.toLocaleString()} of ${totalCount.toLocaleString()}`;
        }
        return `Total: ${displayCount.toLocaleString()}`;
    }, [isFiltered, dataSource.type, generatorSearchResults, displayCount, totalCount]);

    const selectedText = useMemo(() => {
        return selectedIds.size > 0 ? `Selected: ${selectedIds.size.toLocaleString()}` : null;
    }, [selectedIds.size]);

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} className="route-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    // Footer text
    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${displayCount.toLocaleString()}`
        : '';

    // Helper text for sorting
    const helperText = dataSource.type === 'generator' && !canSort ? 'Search to enable sorting' : undefined;

    return (
        <div ref={containerRef} className="route-table" style={{ height: containerHeight }}>
            <TableSearchBar
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                isSearching={isSearching}
                statsText={statsText}
                selectedText={selectedText}
                onClearSelection={handleClearSelection}
                helperText={helperText}
            />

            {/* Table container */}
            <Box className="route-table__wrapper">
                <TableHeader
                    sortState={sortState}
                    onSort={handleSort}
                    canSort={canSort}
                />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="route-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {displayCount === 0 && !isSearching ? (
                        <Box className="route-table__empty">
                            <EmptyState message="No routes found" />
                        </Box>
                    ) : (
                        <div
                            className="route-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map(virtualRow => {
                                // Get route based on data source
                                let route: Route | null;
                                if (displayData) {
                                    // Array mode or filtered generator results
                                    route = displayData[virtualRow.index] || null;
                                } else if (dataSource.type === 'generator') {
                                    // Unfiltered generator mode
                                    try {
                                        route = dataSource.generator.getRoute(virtualRow.index);
                                    } catch {
                                        route = null;
                                    }
                                } else {
                                    route = null;
                                }

                                if (!route) return null;

                                const routeId = getRouteId(route);
                                const isSelected = selectedIds.has(routeId);

                                return (
                                    <VirtualRow
                                        key={virtualRow.index}
                                        route={route}
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
            <Box className="route-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
