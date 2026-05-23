import { useCallback, useEffect, useRef, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { IPAddressWire } from '../../../utils/netip';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { MERGED_TAB } from './types';

const REFRESH_INTERVAL_MS = 5000;

export interface UseNeighboursResult {
    tables: NeighbourTableInfo[];
    cache: Map<string, Neighbour[]>;
    loading: boolean;
    activeTabRef: React.MutableRefObject<string>;
    addNeighbour: (table: string, entry: Neighbour) => Promise<void>;
    updateNeighbour: (table: string, entry: Neighbour) => Promise<void>;
    removeNeighbours: (table: string, nextHopWires: (IPAddressWire | undefined)[]) => Promise<void>;
    createTable: (name: string, priority: number) => Promise<void>;
    updateTable: (name: string, priority: number) => Promise<void>;
    removeTable: (name: string) => Promise<void>;
    reloadAll: () => Promise<void>;
    fetchTab: (tabKey: string) => Promise<void>;
}

/** Loads and manages neighbour tables and their entries with periodic polling. */
export const useNeighbours = (activeTab: string): UseNeighboursResult => {
    const [tables, setTables] = useState<NeighbourTableInfo[]>([]);
    const [cache, setCache] = useState<Map<string, Neighbour[]>>(new Map());
    const [loading, setLoading] = useState(true);

    const activeTabRef = useRef(activeTab);
    activeTabRef.current = activeTab;

    const loadTables = useCallback(async (): Promise<NeighbourTableInfo[]> => {
        try {
            const data = await API.neighbours.listTables();
            const sorted = (data.tables || []).slice().sort((a, b) =>
                (a.name || '').localeCompare(b.name || ''),
            );
            setTables(sorted);
            return sorted;
        } catch (err) {
            toaster.error('nb-tables-error', 'Failed to fetch neighbour tables', err);
            return [];
        }
    }, []);

    const fetchTab = useCallback(async (tabKey: string): Promise<void> => {
        const tableFilter = tabKey === MERGED_TAB ? undefined : tabKey;
        const data = await API.neighbours.list(tableFilter);
        const neighbours = data.neighbours || [];
        setCache((prev) => {
            const next = new Map(prev);
            next.set(tabKey, neighbours);
            return next;
        });
    }, []);

    const prefetchAll = useCallback(async (tableList: NeighbourTableInfo[]): Promise<void> => {
        const keys = [MERGED_TAB, ...tableList.map((t) => t.name || '').filter(Boolean)];
        const results = await Promise.allSettled(
            keys.map(async (key) => {
                const tableFilter = key === MERGED_TAB ? undefined : key;
                const data = await API.neighbours.list(tableFilter);
                return { key, neighbours: data.neighbours || [] };
            }),
        );
        setCache((prev) => {
            const next = new Map(prev);
            for (const result of results) {
                if (result.status === 'fulfilled') {
                    next.set(result.value.key, result.value.neighbours);
                }
            }
            return next;
        });
    }, []);

    const reloadAll = useCallback(async (): Promise<void> => {
        const tableList = await loadTables();
        await prefetchAll(tableList);
    }, [loadTables, prefetchAll]);

    useEffect(() => {
        let isMounted = true;
        const init = async (): Promise<void> => {
            const tableList = await loadTables();
            if (!isMounted) return;
            await prefetchAll(tableList);
            if (isMounted) setLoading(false);
        };
        init();
        return () => { isMounted = false; };
    }, [loadTables, prefetchAll]);

    useEffect(() => {
        if (loading) return;
        const intervalId = window.setInterval(async () => {
            const tab = activeTabRef.current;
            try {
                await fetchTab(tab);
            } catch {
                // Silently ignore periodic refresh errors.
            }
            loadTables();
        }, REFRESH_INTERVAL_MS);
        return () => window.clearInterval(intervalId);
    }, [loading, fetchTab, loadTables]);

    const addNeighbour = useCallback(async (table: string, entry: Neighbour): Promise<void> => {
        try {
            await API.neighbours.updateNeighbours(table, [entry]);
            toaster.success('nb-added', 'Neighbour added.');
            await reloadAll();
        } catch (err) {
            toaster.error('nb-add-error', 'Failed to add neighbour', err);
            throw err;
        }
    }, [reloadAll]);

    const updateNeighbour = useCallback(async (table: string, entry: Neighbour): Promise<void> => {
        try {
            await API.neighbours.updateNeighbours(table, [entry]);
            toaster.success('nb-updated', 'Neighbour updated.');
            await reloadAll();
        } catch (err) {
            toaster.error('nb-update-error', 'Failed to update neighbour', err);
            throw err;
        }
    }, [reloadAll]);

    const removeNeighbours = useCallback(
        async (table: string, nextHopWires: (IPAddressWire | undefined)[]): Promise<void> => {
            const wires = nextHopWires.filter((w): w is IPAddressWire => w !== undefined);
            try {
                await API.neighbours.removeNeighbours(table, wires);
                toaster.success('nb-removed', `${wires.length} neighbour(s) removed.`);
                await reloadAll();
            } catch (err) {
                toaster.error('nb-remove-error', 'Failed to remove neighbours', err);
                throw err;
            }
        },
        [reloadAll],
    );

    const createTable = useCallback(async (name: string, priority: number): Promise<void> => {
        try {
            await API.neighbours.createTable(name, priority);
            toaster.success('nb-table-created', `Table "${name}" created.`);
            await reloadAll();
        } catch (err) {
            toaster.error('nb-table-create-error', 'Failed to create table', err);
            throw err;
        }
    }, [reloadAll]);

    const updateTable = useCallback(async (name: string, priority: number): Promise<void> => {
        try {
            await API.neighbours.updateTable(name, priority);
            toaster.success('nb-table-updated', `Table "${name}" updated.`);
            await loadTables();
        } catch (err) {
            toaster.error('nb-table-update-error', 'Failed to update table', err);
            throw err;
        }
    }, [loadTables]);

    const removeTable = useCallback(async (name: string): Promise<void> => {
        try {
            await API.neighbours.removeTable(name);
            toaster.success('nb-table-removed', `Table "${name}" removed.`);
            await reloadAll();
        } catch (err) {
            toaster.error('nb-table-remove-error', 'Failed to remove table', err);
            throw err;
        }
    }, [reloadAll]);

    return {
        tables,
        cache,
        loading,
        activeTabRef,
        addNeighbour,
        updateNeighbour,
        removeNeighbours,
        createTable,
        updateTable,
        removeTable,
        reloadAll,
        fetchTab,
    };
};
