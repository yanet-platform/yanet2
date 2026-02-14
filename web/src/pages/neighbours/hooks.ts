import { useState, useEffect, useLayoutEffect, useMemo } from 'react';
import type { Neighbour } from '../../api/neighbours';
import { getMACAddressValue, compareMACAddressValues } from '../../utils/mac';
import { compareNullableStrings, getUnixSecondsValue, compareNullableNumbers } from '../../utils/sorting';

// Sorting types
export type SortableColumn =
    | 'next_hop'
    | 'link_addr'
    | 'hardware_addr'
    | 'device'
    | 'state'
    | 'source'
    | 'priority'
    | 'updated_at';

export type SortDirection = 'asc' | 'desc';

export interface SortState {
    column: SortableColumn | null;
    direction: SortDirection;
}

export const SORTABLE_COLUMNS: ReadonlySet<string> = new Set<SortableColumn>([
    'next_hop', 'link_addr', 'hardware_addr', 'device',
    'state', 'source', 'priority', 'updated_at',
]);

export const DEFAULT_SORT: SortState = { column: 'state', direction: 'asc' };

export const isSortableColumn = (value: string): value is SortableColumn =>
    SORTABLE_COLUMNS.has(value);

export const isSortDirection = (value: string): value is SortDirection =>
    value === 'asc' || value === 'desc';

// Sort comparators for neighbour columns
export const sortComparators: Record<SortableColumn, (a: Neighbour, b: Neighbour) => number> = {
    next_hop: (a, b) => compareNullableStrings(a.next_hop, b.next_hop),
    link_addr: (a, b) => compareMACAddressValues(
        getMACAddressValue(a.link_addr?.addr),
        getMACAddressValue(b.link_addr?.addr),
    ),
    hardware_addr: (a, b) => compareMACAddressValues(
        getMACAddressValue(a.hardware_addr?.addr),
        getMACAddressValue(b.hardware_addr?.addr),
    ),
    device: (a, b) => compareNullableStrings(a.device, b.device),
    state: (a, b) => {
        const stateA = a.state ?? 0;
        const stateB = b.state ?? 0;
        if (stateA !== stateB) return stateA - stateB;
        return compareNullableStrings(a.next_hop, b.next_hop);
    },
    source: (a, b) => compareNullableStrings(a.source, b.source),
    priority: (a, b) => (a.priority ?? 0) - (b.priority ?? 0),
    updated_at: (a, b) => compareNullableNumbers(
        getUnixSecondsValue(a.updated_at),
        getUnixSecondsValue(b.updated_at),
    ),
};

// Hook for measuring container height
export const useContainerHeight = (containerRef: React.RefObject<HTMLDivElement | null>) => {
    const [containerHeight, setContainerHeight] = useState(0);

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
    }, [containerRef]);

    return containerHeight;
};

// Hook for filtering and sorting neighbours
export const useProcessedNeighbours = (
    neighbours: Neighbour[],
    searchQuery: string,
    sortState: SortState,
) => {
    return useMemo(() => {
        let result = neighbours;

        // Apply search filter
        if (searchQuery.trim()) {
            const lowerQuery = searchQuery.toLowerCase();
            result = result.filter(n =>
                n.next_hop?.toLowerCase().includes(lowerQuery) ||
                n.device?.toLowerCase().includes(lowerQuery) ||
                n.source?.toLowerCase().includes(lowerQuery),
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
    }, [neighbours, searchQuery, sortState]);
};

// Hook for debounced value
export const useDebouncedValue = (value: string, delay: number = 200): string => {
    const [debouncedValue, setDebouncedValue] = useState(value);

    useEffect(() => {
        const timeoutId = setTimeout(() => {
            setDebouncedValue(value);
        }, delay);

        return () => clearTimeout(timeoutId);
    }, [value, delay]);

    return debouncedValue;
};
