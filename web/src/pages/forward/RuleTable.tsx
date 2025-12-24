import React, { useRef, useCallback, useMemo, useState, useLayoutEffect } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Box, Text } from '@gravity-ui/uikit';
import { EmptyState } from '../../components';
import type { RuleItem, RuleTableProps } from './types';
import { ROW_HEIGHT, OVERSCAN, TOTAL_WIDTH, SEARCH_BAR_HEIGHT, HEADER_HEIGHT, FOOTER_HEIGHT } from './constants';
import { RuleRow } from './RuleRow';
import { RuleSearchBar } from './RuleSearchBar';
import { RuleTableHeader } from './RuleTableHeader';
import { formatDevices, formatIPNets, formatMode } from './hooks';
import './forward.css';

export const RuleTable: React.FC<RuleTableProps> = ({
    rules,
    selectedIds,
    onSelectionChange,
    onEditRule,
}) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const parentRef = useRef<HTMLDivElement>(null);

    const [searchQuery, setSearchQuery] = useState('');
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

    // Filter data
    const processedData = useMemo(() => {
        let result = rules;

        // Apply search filter
        if (searchQuery.trim()) {
            const lowerQuery = searchQuery.toLowerCase();
            result = result.filter((item) => {
                const { rule } = item;
                // Search in target
                if (rule.action?.target?.toLowerCase().includes(lowerQuery)) return true;
                // Search in counter
                if (rule.action?.counter?.toLowerCase().includes(lowerQuery)) return true;
                // Search in mode
                if (formatMode(rule.action?.mode).toLowerCase().includes(lowerQuery)) return true;
                // Search in devices
                if (formatDevices(rule.devices).toLowerCase().includes(lowerQuery)) return true;
                // Search in sources
                if (formatIPNets(rule.srcs).toLowerCase().includes(lowerQuery)) return true;
                // Search in destinations
                if (formatIPNets(rule.dsts).toLowerCase().includes(lowerQuery)) return true;
                return false;
            });
        }

        return result;
    }, [rules, searchQuery]);

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

    const handleRowSelect = useCallback((ruleItem: RuleItem, checked: boolean) => {
        const newSelection = new Set(selectedIds);
        if (checked) {
            newSelection.add(ruleItem.id);
        } else {
            newSelection.delete(ruleItem.id);
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
            onSelectionChange(new Set(processedData.map((r) => r.id)));
        }
    }, [selectedIds.size, processedData, onSelectionChange]);

    const isAllSelected = processedData.length > 0 && selectedIds.size === processedData.length;
    const isIndeterminate = selectedIds.size > 0 && selectedIds.size < processedData.length;

    // Stats
    const statsText = useMemo(() => {
        if (searchQuery.trim()) {
            return `Found: ${processedData.length.toLocaleString()} of ${rules.length.toLocaleString()}`;
        }
        return `Total: ${rules.length.toLocaleString()}`;
    }, [searchQuery, processedData.length, rules.length]);

    const selectedText = useMemo(() => {
        return selectedIds.size > 0 ? `Selected: ${selectedIds.size.toLocaleString()}` : null;
    }, [selectedIds.size]);

    // Don't render until height is measured
    if (containerHeight === 0) {
        return <div ref={containerRef} className="forward-table__container" />;
    }

    const tableBodyHeight = containerHeight - SEARCH_BAR_HEIGHT - HEADER_HEIGHT - FOOTER_HEIGHT - 2;
    const virtualRows = rowVirtualizer.getVirtualItems();

    // Footer text
    const footerText = virtualRows.length > 0
        ? `Rows ${(virtualRows[0].index + 1).toLocaleString()} - ${(virtualRows[virtualRows.length - 1].index + 1).toLocaleString()} of ${processedData.length.toLocaleString()}`
        : '';

    return (
        <div ref={containerRef} className="forward-table" style={{ height: containerHeight }}>
            <RuleSearchBar
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                isSearching={false}
                statsText={statsText}
                selectedText={selectedText}
                onClearSelection={handleClearSelection}
            />

            {/* Table container */}
            <Box className="forward-table__wrapper">
                <RuleTableHeader
                    isAllSelected={isAllSelected}
                    isIndeterminate={isIndeterminate}
                    onSelectAll={handleSelectAll}
                    hasItems={processedData.length > 0}
                />

                {/* Virtualized body */}
                <div
                    ref={parentRef}
                    className="forward-table__body"
                    style={{ height: tableBodyHeight }}
                >
                    {processedData.length === 0 ? (
                        <Box className="forward-table__empty">
                            <EmptyState message="No rules found" />
                        </Box>
                    ) : (
                        <div
                            className="forward-table__virtual-container"
                            style={{
                                height: rowVirtualizer.getTotalSize(),
                                minWidth: TOTAL_WIDTH,
                            }}
                        >
                            {virtualRows.map((virtualRow) => {
                                const ruleItem = processedData[virtualRow.index];
                                if (!ruleItem) return null;

                                const isSelected = selectedIds.has(ruleItem.id);

                                return (
                                    <RuleRow
                                        key={ruleItem.id}
                                        ruleItem={ruleItem}
                                        start={virtualRow.start}
                                        isSelected={isSelected}
                                        onSelect={handleRowSelect}
                                        onEdit={onEditRule}
                                    />
                                );
                            })}
                        </div>
                    )}
                </div>
            </Box>

            {/* Footer */}
            <Box className="forward-table__footer" style={{ height: FOOTER_HEIGHT }}>
                <Text variant="body-2" color="secondary">{footerText}</Text>
                <Text variant="body-2" color="secondary">Scroll to navigate</Text>
            </Box>
        </div>
    );
};
