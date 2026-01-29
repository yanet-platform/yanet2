import { useMemo, useCallback, useState, useRef, useLayoutEffect } from 'react';
import type { TableColumnConfig } from '@gravity-ui/uikit';
import type { Route } from '../../api/routes';
import type { ConfigRoutesData, SortState, SortableColumn } from './types';
import { ROUTE_SOURCES } from './constants';
import {
    compareBooleans,
    compareNullableNumbers,
    compareNullableStrings,
} from '../../utils/sorting';

/**
 * Hook that returns table column configuration for routes
 */
export const useRouteColumns = (): TableColumnConfig<Route>[] => {
    return useMemo(() => [
        {
            id: 'prefix',
            name: 'Prefix',
            meta: {
                sort: (a: Route, b: Route) => compareNullableStrings(a.prefix, b.prefix),
            },
            template: (item: Route) => item.prefix || '-',
        },
        {
            id: 'next_hop',
            name: 'Next Hop',
            meta: {
                sort: (a: Route, b: Route) => compareNullableStrings(a.next_hop, b.next_hop),
            },
            template: (item: Route) => item.next_hop || '-',
        },
        {
            id: 'peer',
            name: 'Peer',
            meta: {
                sort: (a: Route, b: Route) => compareNullableStrings(a.peer, b.peer),
            },
            template: (item: Route) => item.peer || '-',
        },
        {
            id: 'is_best',
            name: 'Best',
            meta: {
                sort: (a: Route, b: Route) => compareBooleans(a.is_best, b.is_best),
            },
            template: (item: Route) => item.is_best ? 'Yes' : 'No',
        },
        {
            id: 'pref',
            name: 'Preference',
            meta: {
                sort: (a: Route, b: Route) => compareNullableNumbers(a.pref, b.pref),
            },
            template: (item: Route) => item.pref?.toString() || '-',
        },
        {
            id: 'as_path_len',
            name: 'AS Path Len',
            meta: {
                sort: (a: Route, b: Route) => compareNullableNumbers(a.as_path_len, b.as_path_len),
            },
            template: (item: Route) => item.as_path_len?.toString() || '-',
        },
        {
            id: 'source',
            name: 'Source',
            meta: {
                sort: (a: Route, b: Route) => compareNullableNumbers(a.source, b.source),
            },
            template: (item: Route) => {
                if (item.source === undefined) return '-';
                return ROUTE_SOURCES[item.source] || 'Unknown';
            },
        },
    ], []);
};

/**
 * Hook for managing route config data with routes map and selection map
 */
export const useConfigRoutesData = (
    configRoutesMap: Map<string, Route[]>,
    selectedRoutes: Map<string, Set<string>>
): (configName: string) => ConfigRoutesData => {
    return useCallback((configName: string): ConfigRoutesData => {
        const routes = configRoutesMap.get(configName) || [];
        const selectedSet = selectedRoutes.get(configName) || new Set<string>();
        return {
            routes,
            selectedIds: Array.from(selectedSet),
        };
    }, [configRoutesMap, selectedRoutes]);
};

/**
 * Sort comparators for route columns
 */
export const sortComparators: Record<SortableColumn, (a: Route, b: Route) => number> = {
    prefix: (a, b) => (a.prefix || '').localeCompare(b.prefix || ''),
    next_hop: (a, b) => (a.next_hop || '').localeCompare(b.next_hop || ''),
    peer: (a, b) => (a.peer || '').localeCompare(b.peer || ''),
    is_best: (a, b) => (a.is_best ? 1 : 0) - (b.is_best ? 1 : 0),
    pref: (a, b) => (a.pref ?? 0) - (b.pref ?? 0),
    as_path_len: (a, b) => (a.as_path_len ?? 0) - (b.as_path_len ?? 0),
    source: (a, b) => (a.source ?? 0) - (b.source ?? 0),
};

/**
 * Hook for managing sort state
 */
export const useSortState = () => {
    const [sortState, setSortState] = useState<SortState>({ column: null, direction: 'asc' });

    const handleSort = useCallback((column: SortableColumn) => {
        setSortState(prev => ({
            column,
            direction: prev.column === column && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
    }, []);

    const sortData = useCallback((data: Route[]): Route[] => {
        if (!sortState.column) return data;

        const comparator = sortComparators[sortState.column];
        const sorted = [...data].sort(comparator);

        return sortState.direction === 'desc' ? sorted.reverse() : sorted;
    }, [sortState]);

    return { sortState, handleSort, sortData };
};

/**
 * Hook for measuring container height
 */
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

/**
 * Hook for debounced search
 */
export const useDebouncedSearch = <T>(
    searchFn: (query: string) => T,
    delay: number = 300
): {
    isSearching: boolean;
    results: T | null;
    search: (query: string) => void;
    clear: () => void;
} => {
    const [isSearching, setIsSearching] = useState(false);
    const [results, setResults] = useState<T | null>(null);
    const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const search = useCallback((query: string) => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }

        if (!query.trim()) {
            setResults(null);
            setIsSearching(false);
            return;
        }

        setIsSearching(true);
        timeoutRef.current = setTimeout(() => {
            const searchResults = searchFn(query);
            setResults(searchResults);
            setIsSearching(false);
        }, delay);
    }, [searchFn, delay]);

    const clear = useCallback(() => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }
        setResults(null);
        setIsSearching(false);
    }, []);

    return { isSearching, results, search, clear };
};
