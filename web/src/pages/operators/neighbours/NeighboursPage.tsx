import React, { useCallback, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { BulkDeleteModal, DeleteConfigModal } from '../../_shared/draft';
import { stringToIPAddress } from '../../../utils/netip';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { NeighbourTable } from './NeighbourTable';
import NeighbourDrawer from './NeighbourDrawer';
import CreateTableModal from './CreateTableModal';
import EditTableModal from './EditTableModal';
import { useNeighbours } from './useNeighbours';
import { getNeighbourId, isSortableColumn, isSortDirection, sortComparators } from './utils';
import { MERGED_TAB, DEFAULT_SORT } from './types';
import type { SortState, SortableColumn } from './types';
import '../../../styles/draft-page.scss';

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

const parseTab = (params: URLSearchParams): string =>
    params.get(QP_TAB) || MERGED_TAB;

const parseSearch = (params: URLSearchParams): string =>
    params.get(QP_SEARCH) || '';

/** Neighbours page — shows neighbour tables and entries with inline drawer editing. */
const NeighboursPage: React.FC = () => {
    const [searchParams, setSearchParams] = useSearchParams();

    const activeTab = parseTab(searchParams);
    const sortState = parseSortState(searchParams);
    const search = parseSearch(searchParams);

    const {
        tables,
        cache,
        loading,
        addNeighbour,
        updateNeighbour,
        removeNeighbours,
        createTable,
        updateTable,
        removeTable,
        reloadAll,
        fetchTab,
    } = useNeighbours(activeTab);

    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [drawer, setDrawer] = useState<{ open: boolean; mode: 'add' | 'edit'; neighbour: Neighbour | null }>({
        open: false,
        mode: 'add',
        neighbour: null,
    });
    const [bulkRemoveOpen, setBulkRemoveOpen] = useState(false);
    const [rowDeleteConfirm, setRowDeleteConfirm] = useState<{ open: boolean; neighbour: Neighbour | null }>({
        open: false,
        neighbour: null,
    });
    const [createTableOpen, setCreateTableOpen] = useState(false);
    const [editTableOpen, setEditTableOpen] = useState(false);
    const [deleteTableOpen, setDeleteTableOpen] = useState(false);
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);

    const searchRef = useRef<HTMLInputElement>(null);

    const isMergedView = activeTab === MERGED_TAB;
    const activeTableInfo: NeighbourTableInfo | null = tables.find((t) => t.name === activeTab) ?? null;
    const isBuiltIn = activeTableInfo?.built_in ?? false;

    const tabsList = [MERGED_TAB, ...tables.map((t) => t.name || '').filter(Boolean)];

    const updateParams = useCallback((updates: Record<string, string | null>): void => {
        setSearchParams((prev) => {
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

    const handleTabSelect = useCallback((cfg: string): void => {
        const tab = cfg === MERGED_TAB ? MERGED_TAB : cfg;
        updateParams({ [QP_TAB]: tab === MERGED_TAB ? null : tab });
        setSelectedIds(new Set());
        setActiveRowId(null);
        setEditingRowId(null);
        fetchTab(tab).catch(() => {});
    }, [updateParams, fetchTab]);

    const handleSort = useCallback((col: SortableColumn): void => {
        const newDirection: SortState['direction'] =
            sortState.column === col && sortState.direction === 'asc' ? 'desc' : 'asc';
        updateParams({ [QP_SORT]: col, [QP_ORDER]: newDirection });
    }, [sortState, updateParams]);

    const allRows = cache.get(activeTab) || [];

    const visibleRows = useMemo(() => {
        let res = allRows;
        const q = search.trim().toLowerCase();
        if (q) {
            res = res.filter((n) =>
                (getNeighbourId(n) || '').toLowerCase().includes(q) ||
                (n.device || '').toLowerCase().includes(q) ||
                (n.source || '').toLowerCase().includes(q),
            );
        }
        if (sortState.column) {
            const cmp = sortComparators[sortState.column];
            res = [...res].sort(sortState.direction === 'desc' ? (a, b) => cmp(b, a) : cmp);
        }
        return res;
    }, [allRows, search, sortState]);

    const counts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        m.set(MERGED_TAB, tables.reduce((sum, t) => sum + Number(t.entry_count ?? 0), 0));
        tables.forEach((t) => {
            if (t.name) m.set(t.name, Number(t.entry_count ?? 0));
        });
        return m;
    }, [tables]);

    const openAdd = useCallback((): void => {
        setDrawer({ open: true, mode: 'add', neighbour: null });
    }, []);

    const handleEditRow = useCallback((id: string): void => {
        const neighbour = allRows.find((n) => getNeighbourId(n) === id) || null;
        setDrawer({ open: true, mode: 'edit', neighbour });
        setActiveRowId(id);
        setEditingRowId(id);
    }, [allRows]);

    const handleCloseDrawer = useCallback((): void => {
        setDrawer((prev) => ({ ...prev, open: false }));
        setEditingRowId(null);
    }, []);

    const handleRowClick = useCallback((id: string): void => {
        setActiveRowId(id);
    }, []);

    const handleSubmitNeighbour = useCallback(async (table: string, entry: Neighbour): Promise<void> => {
        if (drawer.mode === 'add') {
            await addNeighbour(table, entry);
        } else {
            await updateNeighbour(table, entry);
        }
    }, [drawer.mode, addNeighbour, updateNeighbour]);

    const handleDeleteNeighbour = useCallback(async (neighbour: Neighbour): Promise<void> => {
        const table = isMergedView ? (neighbour.source || 'static') : activeTab;
        const wire = stringToIPAddress(getNeighbourId(neighbour));
        if (!wire) return;
        await removeNeighbours(table, [wire]);
        setSelectedIds(new Set());
    }, [isMergedView, activeTab, removeNeighbours]);

    const handleDeleteRowRequest = useCallback((id: string): void => {
        const neighbour = allRows.find((n) => getNeighbourId(n) === id) || null;
        if (neighbour) setRowDeleteConfirm({ open: true, neighbour });
    }, [allRows]);

    const handleDeleteRowConfirm = useCallback(async (): Promise<void> => {
        const neighbour = rowDeleteConfirm.neighbour;
        setRowDeleteConfirm({ open: false, neighbour: null });
        if (!neighbour) return;
        await handleDeleteNeighbour(neighbour);
    }, [rowDeleteConfirm.neighbour, handleDeleteNeighbour]);

    const handleBulkRemove = useCallback(async (): Promise<void> => {
        if (isMergedView || !activeTab) return;
        const wires = Array.from(selectedIds).map((s) => stringToIPAddress(s));
        await removeNeighbours(activeTab, wires);
        setSelectedIds(new Set());
        setBulkRemoveOpen(false);
    }, [isMergedView, activeTab, selectedIds, removeNeighbours]);

    const handleCreateTable = useCallback(async (name: string, priority: number): Promise<void> => {
        await createTable(name, priority);
        updateParams({ [QP_TAB]: name });
    }, [createTable, updateParams]);

    const handleEditTable = useCallback(async (name: string, priority: number): Promise<void> => {
        await updateTable(name, priority);
        setEditTableOpen(false);
    }, [updateTable]);

    const handleDeleteTable = useCallback(async (): Promise<void> => {
        if (!activeTableInfo?.name) return;
        await removeTable(activeTableInfo.name);
        updateParams({ [QP_TAB]: null });
        setSelectedIds(new Set());
        setDeleteTableOpen(false);
        await reloadAll();
    }, [activeTableInfo, removeTable, updateParams, reloadAll]);

    const canEditTable = !isMergedView && !!activeTableInfo;
    const canDeleteTable = !isMergedView && !!activeTableInfo && !isBuiltIn;

    const defaultAddTable = useMemo(() => {
        if (!isMergedView) return activeTab;
        const staticTable = tables.find((t) => t.name === 'static');
        if (staticTable) return 'static';
        const firstNonBuiltin = tables.find((t) => !t.built_in && t.name);
        return firstNonBuiltin?.name || tables[0]?.name || '';
    }, [isMergedView, activeTab, tables]);

    const displayLabel = (cfg: string): string => cfg === MERGED_TAB ? 'Merged' : cfg;

    const displayConfigs = tabsList.map(displayLabel);
    const activeDisplayConfig = displayLabel(activeTab);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Neighbours</Text>
            <Flex grow />
            <div style={{ flexBasis: 360, flexShrink: 1 }}>
                <TextInput
                    controlRef={searchRef as React.RefObject<HTMLInputElement | null>}
                    value={search}
                    onUpdate={(v) => updateParams({ [QP_SEARCH]: v || null })}
                    placeholder="Search next hop, device, source… (/)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            <Button view="outlined" onClick={() => setCreateTableOpen(true)}>
                <Icon data={Plus} size={16} />
                Add Table
            </Button>
            <Button view="action" onClick={openAdd} disabled={tables.length === 0}>
                <Icon data={Plus} size={16} />
                Add Neighbour
            </Button>
        </Flex>
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader}>
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader}>
            <div className="fw-page">
                {tables.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No neighbour tables found.</div>
                        <Button view="action" onClick={() => setCreateTableOpen(true)}>Create table</Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={displayConfigs}
                            activeConfig={activeDisplayConfig}
                            counts={(() => {
                                const m = new Map<string, number>();
                                tabsList.forEach((t) => {
                                    m.set(displayLabel(t), counts.get(t) ?? 0);
                                });
                                return m;
                            })()}
                            dirtyConfigs={new Set()}
                            onSelect={(label) => {
                                const tab = label === 'Merged' ? MERGED_TAB : label;
                                handleTabSelect(tab);
                            }}
                            onAddConfig={() => setCreateTableOpen(true)}
                            addLabel="Add table"
                        />
                        <div className="fw-content">
                            <NeighbourTable
                                rows={visibleRows}
                                selectedIds={selectedIds}
                                activeRowId={activeRowId}
                                editingRowId={editingRowId}
                                sortState={sortState}
                                onSort={handleSort}
                                onRowClick={handleRowClick}
                                onEditRow={handleEditRow}
                                onSelectionChange={setSelectedIds}
                                emptyMessage={search ? 'No neighbours match your search.' : 'No neighbours.'}
                                canEditTable={canEditTable}
                                canDeleteTable={canDeleteTable}
                                onEditTable={() => setEditTableOpen(true)}
                                onDeleteTable={() => setDeleteTableOpen(true)}
                                onDeleteRow={isMergedView ? undefined : handleDeleteRowRequest}
                                canEditRow={!isMergedView}
                            />
                        </div>
                    </>
                )}

                {selectedIds.size > 0 && !isMergedView && (
                    <BulkBar
                        count={selectedIds.size}
                        itemNoun="neighbour"
                        onDelete={() => setBulkRemoveOpen(true)}
                        onClear={() => setSelectedIds(new Set())}
                    />
                )}

                <BulkDeleteModal
                    open={bulkRemoveOpen}
                    count={selectedIds.size}
                    itemNoun="neighbour"
                    configName={activeTab}
                    onClose={() => setBulkRemoveOpen(false)}
                    onConfirm={handleBulkRemove}
                    immediate
                />

                <BulkDeleteModal
                    open={rowDeleteConfirm.open}
                    count={1}
                    itemNoun="neighbour"
                    configName={activeTableInfo?.name || activeTab}
                    onClose={() => setRowDeleteConfirm({ open: false, neighbour: null })}
                    onConfirm={handleDeleteRowConfirm}
                    immediate
                />

                <DeleteConfigModal
                    open={deleteTableOpen}
                    configName={activeTableInfo?.name || ''}
                    onClose={() => setDeleteTableOpen(false)}
                    onConfirm={handleDeleteTable}
                />

                <NeighbourDrawer
                    open={drawer.open}
                    mode={drawer.mode}
                    tables={tables}
                    defaultTable={defaultAddTable}
                    neighbour={drawer.neighbour}
                    activeTable={activeTab}
                    onClose={handleCloseDrawer}
                    onSubmit={handleSubmitNeighbour}
                    onDelete={drawer.mode === 'edit' ? handleDeleteNeighbour : undefined}
                />

                <CreateTableModal
                    open={createTableOpen}
                    onClose={() => setCreateTableOpen(false)}
                    onCreate={handleCreateTable}
                    existingNames={tables.map((t) => t.name || '')}
                />

                <EditTableModal
                    open={editTableOpen}
                    onClose={() => setEditTableOpen(false)}
                    onSave={handleEditTable}
                    tableInfo={activeTableInfo}
                />
            </div>
        </PageLayout>
    );
};

export default NeighboursPage;
