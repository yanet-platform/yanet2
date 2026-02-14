import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Box, TabProvider, TabList, Tab, Button, Flex, Text } from '@gravity-ui/uikit';
import { toaster } from '../utils';
import { API } from '../api';
import type { Neighbour, NeighbourTableInfo } from '../api/neighbours';
import { PageLayout, PageLoader } from '../components';
import {
    AddNeighbourDialog,
    EditNeighbourDialog,
    RemoveNeighboursDialog,
    CreateTableDialog,
    EditTableDialog,
    RemoveTableDialog,
    VirtualizedNeighbourTable,
} from './neighbours';
import {
    DEFAULT_SORT,
    isSortableColumn,
    isSortDirection,
} from './neighbours/hooks';
import type { SortState, SortableColumn, SortDirection } from './neighbours/hooks';
import './neighbours/neighbours.scss';

const REFRESH_INTERVAL_MS = 5000;
const MERGED_TAB = '__merged__';

// URL query param keys
const QP_TAB = 'tab';
const QP_SORT = 'sort';
const QP_ORDER = 'order';
const QP_SEARCH = 'search';

const parseSortState = (params: URLSearchParams): SortState => {
    const col = params.get(QP_SORT);
    const dir = params.get(QP_ORDER);
    if (col && isSortableColumn(col)) {
        return {
            column: col,
            direction: dir && isSortDirection(dir) ? dir : 'asc',
        };
    }
    return DEFAULT_SORT;
};

const parseTab = (params: URLSearchParams): string => {
    return params.get(QP_TAB) || MERGED_TAB;
};

const parseSearch = (params: URLSearchParams): string => {
    return params.get(QP_SEARCH) || '';
};

const NeighboursPage = (): React.JSX.Element => {
    const [searchParams, setSearchParams] = useSearchParams();

    // Derive state from URL
    const activeTab = parseTab(searchParams);
    const sortState = parseSortState(searchParams);
    const searchQuery = parseSearch(searchParams);

    const [tables, setTables] = useState<NeighbourTableInfo[]>([]);
    const [initialLoading, setInitialLoading] = useState<boolean>(true);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

    // Cache: tab key -> neighbours list. Merged tab uses MERGED_TAB key.
    const [cache, setCache] = useState<Map<string, Neighbour[]>>(new Map());

    // Dialog states
    const [addDialogOpen, setAddDialogOpen] = useState(false);
    const [editDialogOpen, setEditDialogOpen] = useState(false);
    const [removeDialogOpen, setRemoveDialogOpen] = useState(false);
    const [createTableDialogOpen, setCreateTableDialogOpen] = useState(false);
    const [editTableDialogOpen, setEditTableDialogOpen] = useState(false);
    const [removeTableDialogOpen, setRemoveTableDialogOpen] = useState(false);
    const [editingNeighbour, setEditingNeighbour] = useState<Neighbour | null>(null);

    // Ref to track the active tab for async operations
    const activeTabRef = useRef(activeTab);
    activeTabRef.current = activeTab;

    // Helper to update URL params without replacing other params
    const updateParams = useCallback((updates: Record<string, string | null>) => {
        setSearchParams(prev => {
            const next = new URLSearchParams(prev);
            for (const [key, value] of Object.entries(updates)) {
                if (value === null || value === '') {
                    next.delete(key);
                } else {
                    next.set(key, value);
                }
            }
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    const handleSort = useCallback((column: SortableColumn) => {
        const newDirection: SortDirection =
            sortState.column === column && sortState.direction === 'asc' ? 'desc' : 'asc';
        updateParams({
            [QP_SORT]: column,
            [QP_ORDER]: newDirection,
        });
    }, [sortState, updateParams]);

    const handleSearchChange = useCallback((query: string) => {
        updateParams({ [QP_SEARCH]: query || null });
    }, [updateParams]);

    const loadTables = useCallback(async (): Promise<NeighbourTableInfo[]> => {
        try {
            const data = await API.neighbours.listTables();
            const sorted = (data.tables || []).slice().sort((a, b) =>
                (a.name || '').localeCompare(b.name || ''),
            );
            setTables(sorted);
            return sorted;
        } catch (err) {
            toaster.error('tables-error', 'Failed to fetch neighbour tables', err);
            return [];
        }
    }, []);

    // Fetch neighbours for a specific tab and update cache
    const fetchNeighbours = useCallback(async (tabKey: string): Promise<Neighbour[]> => {
        const tableFilter = tabKey === MERGED_TAB ? undefined : tabKey;
        const data = await API.neighbours.list(tableFilter);
        const neighbours = data.neighbours || [];
        setCache(prev => {
            const next = new Map(prev);
            next.set(tabKey, neighbours);
            return next;
        });
        return neighbours;
    }, []);

    // Prefetch all tables in background
    const prefetchAll = useCallback(async (tableList: NeighbourTableInfo[]) => {
        const keys = [MERGED_TAB, ...tableList.map(t => t.name || '').filter(Boolean)];
        const results = await Promise.allSettled(
            keys.map(async (key) => {
                const tableFilter = key === MERGED_TAB ? undefined : key;
                const data = await API.neighbours.list(tableFilter);
                return { key, neighbours: data.neighbours || [] };
            }),
        );

        setCache(prev => {
            const next = new Map(prev);
            for (const result of results) {
                if (result.status === 'fulfilled') {
                    next.set(result.value.key, result.value.neighbours);
                }
            }
            return next;
        });
    }, []);

    // Initial load: tables + prefetch all
    useEffect(() => {
        let isMounted = true;

        const init = async () => {
            const tableList = await loadTables();
            if (!isMounted) return;
            await prefetchAll(tableList);
            if (isMounted) {
                setInitialLoading(false);
            }
        };

        init();
        return () => { isMounted = false; };
    }, [loadTables, prefetchAll]);

    // Periodic refresh: update active tab + tables
    useEffect(() => {
        if (initialLoading) return;

        const intervalId = window.setInterval(async () => {
            const tab = activeTabRef.current;
            try {
                await fetchNeighbours(tab);
            } catch {
                // Silently ignore periodic refresh errors
            }
            loadTables();
        }, REFRESH_INTERVAL_MS);

        return () => window.clearInterval(intervalId);
    }, [initialLoading, fetchNeighbours, loadTables]);

    // On tab switch: immediately show cached data, then refresh in background
    const handleTabChange = useCallback((tab: string) => {
        updateParams({
            [QP_TAB]: tab === MERGED_TAB ? null : tab,
        });
        setSelectedIds(new Set());
        // Refresh data for the new tab in background
        fetchNeighbours(tab).catch(() => { });
        loadTables();
    }, [fetchNeighbours, loadTables, updateParams]);

    const neighbours = cache.get(activeTab) || [];

    const isMergedView = activeTab === MERGED_TAB;

    const activeTableInfo = useMemo(
        () => tables.find((t) => t.name === activeTab) ?? null,
        [tables, activeTab],
    );

    const isBuiltIn = activeTableInfo?.built_in ?? false;

    // Reload all data (after mutations)
    const reloadAll = useCallback(async () => {
        const tableList = await loadTables();
        await prefetchAll(tableList);
    }, [loadTables, prefetchAll]);

    // Entry actions
    const handleAddConfirm = useCallback(async (table: string, entry: Neighbour) => {
        try {
            await API.neighbours.updateNeighbours(table, [entry]);
            toaster.success('neighbour-added', 'Neighbour added successfully');
            await reloadAll();
        } catch (err) {
            toaster.error('neighbour-add-error', 'Failed to add neighbour', err);
            throw err;
        }
    }, [reloadAll]);

    const handleEditConfirm = useCallback(async (table: string, entry: Neighbour) => {
        try {
            await API.neighbours.updateNeighbours(table, [entry]);
            toaster.success('neighbour-updated', 'Neighbour updated successfully');
            await reloadAll();
        } catch (err) {
            toaster.error('neighbour-edit-error', 'Failed to update neighbour', err);
            throw err;
        }
    }, [reloadAll]);

    const handleRemoveConfirm = useCallback(async () => {
        if (isMergedView || !activeTab) return;
        try {
            const nextHops = Array.from(selectedIds);
            await API.neighbours.removeNeighbours(activeTab, nextHops);
            toaster.success('neighbours-removed', `${nextHops.length} neighbour(s) removed`);
            setSelectedIds(new Set());
            await reloadAll();
        } catch (err) {
            toaster.error('neighbours-remove-error', 'Failed to remove neighbours', err);
            throw err;
        }
    }, [isMergedView, activeTab, selectedIds, reloadAll]);

    // Table actions
    const handleCreateTableConfirm = useCallback(async (name: string, defaultPriority: number) => {
        try {
            await API.neighbours.createTable(name, defaultPriority);
            toaster.success('table-created', `Table "${name}" created`);
            await reloadAll();
            updateParams({ [QP_TAB]: name });
        } catch (err) {
            toaster.error('table-create-error', 'Failed to create table', err);
            throw err;
        }
    }, [reloadAll, updateParams]);

    const handleEditTableConfirm = useCallback(async (name: string, defaultPriority: number) => {
        try {
            await API.neighbours.updateTable(name, defaultPriority);
            toaster.success('table-updated', `Table "${name}" updated`);
            await loadTables();
        } catch (err) {
            toaster.error('table-edit-error', 'Failed to update table', err);
            throw err;
        }
    }, [loadTables]);

    const handleRemoveTableConfirm = useCallback(async () => {
        if (!activeTableInfo?.name) return;
        try {
            await API.neighbours.removeTable(activeTableInfo.name);
            toaster.success('table-removed', `Table "${activeTableInfo.name}" removed`);
            updateParams({ [QP_TAB]: null });
            setSelectedIds(new Set());
            await reloadAll();
        } catch (err) {
            toaster.error('table-remove-error', 'Failed to remove table', err);
            throw err;
        }
    }, [activeTableInfo, reloadAll, updateParams]);

    const handleEditClick = useCallback((item: Neighbour) => {
        setEditingNeighbour(item);
        setEditDialogOpen(true);
    }, []);

    const editTable = useCallback(() => {
        return isMergedView ? (editingNeighbour?.source || 'static') : activeTab;
    }, [isMergedView, editingNeighbour, activeTab]);

    const handleSelectionChange = useCallback((ids: string[]) => {
        setSelectedIds(new Set(ids));
    }, []);

    // Determine default table for Add dialog
    const defaultAddTable = isMergedView ? 'static' : activeTab;

    const headerContent = (
        <Flex gap={2} alignItems="center" style={{ width: '100%' }}>
            <Text variant="header-1">Neighbours</Text>
            <Box style={{ flex: 1 }} />
            <Button view="action" onClick={() => setAddDialogOpen(true)}>
                Add Neighbour
            </Button>
            <Button
                view="outlined-danger"
                onClick={() => setRemoveDialogOpen(true)}
                disabled={isMergedView || selectedIds.size === 0}
            >
                Remove Selected
            </Button>
            <Box style={{ width: 1, height: 24, backgroundColor: 'var(--g-color-line-generic)' }} />
            <Button view="normal" onClick={() => setCreateTableDialogOpen(true)}>
                Create Table
            </Button>
            {!isMergedView && (
                <>
                    <Button
                        view="normal"
                        onClick={() => setEditTableDialogOpen(true)}
                    >
                        Edit Table
                    </Button>
                    <Button
                        view="outlined-danger"
                        onClick={() => setRemoveTableDialogOpen(true)}
                        disabled={isBuiltIn}
                    >
                        Remove Table
                    </Button>
                </>
            )}
        </Flex>
    );

    if (initialLoading) {
        return (
            <PageLayout title="Neighbours">
                <PageLoader loading={initialLoading} size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Flex direction="column" className="neigh-page__content">
                <TabProvider value={activeTab} onUpdate={handleTabChange}>
                    <TabList>
                        <Tab value={MERGED_TAB}>
                            Merged
                        </Tab>
                        {tables.map((t) => (
                            <Tab key={t.name} value={t.name || ''}>
                                {t.name} ({Number(t.entry_count ?? 0)})
                            </Tab>
                        ))}
                    </TabList>
                </TabProvider>

                <Box spacing={{ mt: 2 }} style={{ flex: 1, minHeight: 0 }}>
                    <VirtualizedNeighbourTable
                        neighbours={neighbours}
                        selectedIds={selectedIds}
                        onSelectionChange={handleSelectionChange}
                        onEditNeighbour={handleEditClick}
                        sortState={sortState}
                        onSort={handleSort}
                        searchQuery={searchQuery}
                        onSearchChange={handleSearchChange}
                    />
                </Box>
            </Flex>

            {/* Entry dialogs */}
            <AddNeighbourDialog
                open={addDialogOpen}
                onClose={() => setAddDialogOpen(false)}
                onConfirm={handleAddConfirm}
                tables={tables}
                defaultTable={defaultAddTable}
            />

            <EditNeighbourDialog
                open={editDialogOpen}
                onClose={() => {
                    setEditDialogOpen(false);
                    setEditingNeighbour(null);
                }}
                onConfirm={handleEditConfirm}
                neighbour={editingNeighbour}
                table={editTable()}
            />

            <RemoveNeighboursDialog
                open={removeDialogOpen}
                onClose={() => setRemoveDialogOpen(false)}
                onConfirm={handleRemoveConfirm}
                selectedCount={selectedIds.size}
            />

            {/* Table dialogs */}
            <CreateTableDialog
                open={createTableDialogOpen}
                onClose={() => setCreateTableDialogOpen(false)}
                onConfirm={handleCreateTableConfirm}
            />

            <EditTableDialog
                open={editTableDialogOpen}
                onClose={() => setEditTableDialogOpen(false)}
                onConfirm={handleEditTableConfirm}
                tableInfo={activeTableInfo}
            />

            <RemoveTableDialog
                open={removeTableDialogOpen}
                onClose={() => setRemoveTableDialogOpen(false)}
                onConfirm={handleRemoveTableConfirm}
                tableName={activeTableInfo?.name || ''}
            />
        </PageLayout>
    );
};

export default NeighboursPage;
