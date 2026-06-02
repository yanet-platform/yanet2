import React, { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { Plus, Layers } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput } from '../../../components';
import { BulkDeleteModal, DeleteConfigModal } from '../../_shared/draft';
import { stringToIPAddress, ipAddressToString } from '../../../utils/netip';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { NeighbourTable } from './NeighbourTable';
import NeighbourPanel from './NeighbourPanel';
import CreateTableModal from './CreateTableModal';
import EditTableModal from './EditTableModal';
import { useNeighbours } from './useNeighbours';
import { getNeighbourId, isSortableColumn, isSortDirection, sortComparators } from './utils';
import { MERGED_TAB, DEFAULT_SORT } from './types';
import type { SortState, SortableColumn } from './types';
import { FamilyFilter, type IPFamily } from '../../_shared/table/FamilyFilter';
import '../../../styles/draft-page.scss';

const QP_TAB = 'tab';
const QP_SORT = 'sort';
const QP_ORDER = 'order';
const QP_SEARCH = 'search';
const QP_FAMILY = 'family';

const parseFamily = (params: URLSearchParams): IPFamily => {
    const v = params.get(QP_FAMILY);
    if (v === 'v4' || v === 'v6') return v;
    return 'all';
};

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

/** Neighbours page — shows neighbour tables and entries with inline panel editing. */
const NeighboursPage: React.FC = () => {
    const [searchParams, setSearchParams] = useSearchParams();

    const activeTab = parseTab(searchParams);
    const sortState = parseSortState(searchParams);
    const search = parseSearch(searchParams);
    const family = parseFamily(searchParams);

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
    const [panel, setPanel] = useState<{ open: boolean; mode: 'add' | 'edit' | 'view'; neighbour: Neighbour | null }>({
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
        setPanel((prev) => ({ ...prev, open: false }));
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
        if (family !== 'all') {
            res = res.filter((n) => {
                const addr = ipAddressToString(n.next_hop);
                return family === 'v6' ? addr.includes(':') : !addr.includes(':');
            });
        }
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
    }, [allRows, search, sortState, family]);

    const counts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        m.set(MERGED_TAB, tables.reduce((sum, t) => sum + Number(t.entry_count ?? 0), 0));
        tables.forEach((t) => {
            if (t.name) m.set(t.name, Number(t.entry_count ?? 0));
        });
        return m;
    }, [tables]);

    const openAdd = useCallback((): void => {
        setPanel({ open: true, mode: 'add', neighbour: null });
    }, []);

    const handleEditRow = useCallback((id: string): void => {
        const neighbour = allRows.find((n) => getNeighbourId(n) === id) || null;
        setPanel({ open: true, mode: 'edit', neighbour });
    }, [allRows]);

    const handleClosePanel = useCallback((): void => {
        setPanel((prev) => ({ ...prev, open: false }));
    }, []);

    const handleRowClick = useCallback((id: string): void => {
        const neighbour = allRows.find((n) => getNeighbourId(n) === id) || null;
        const mode = isMergedView ? 'view' : 'edit';
        setPanel({ open: true, mode, neighbour });
    }, [allRows, isMergedView]);

    const handleSubmitNeighbour = useCallback(async (table: string, entry: Neighbour): Promise<void> => {
        if (panel.mode === 'add') {
            await addNeighbour(table, entry);
        } else {
            await updateNeighbour(table, entry);
        }
    }, [panel.mode, addNeighbour, updateNeighbour]);

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
        handleClosePanel();
    }, [rowDeleteConfirm.neighbour, handleDeleteNeighbour, handleClosePanel]);

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

    const handlePinAsStatic = useCallback((neighbour: Neighbour): void => {
        updateParams({ [QP_TAB]: 'static' });
        fetchTab('static').catch(() => {});
        setPanel({ open: true, mode: 'add', neighbour });
    }, [updateParams, fetchTab]);

    const displayLabel = (cfg: string): string => cfg === MERGED_TAB ? 'Merged' : cfg;

    const displayConfigs = tabsList.map(displayLabel);
    const activeDisplayConfig = displayLabel(activeTab);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Neighbours</Text>
            <Flex grow />
            <div style={{ flexBasis: 360, flexShrink: 1 }}>
                <SearchInput
                    value={search}
                    onUpdate={(v) => updateParams({ [QP_SEARCH]: v || null })}
                    placeholder="Search next hop, device, source…"
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
            <PageLayout header={pageHeader} className="nb-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="nb-layout">
            <div className="yn-page nb-page">
                {tables.length === 0 ? (
                    <div className="yn-empty-page">
                        <div className="yn-empty-page__message">No neighbour tables found.</div>
                        <Button view="action" onClick={() => setCreateTableOpen(true)}>Create table</Button>
                    </div>
                ) : (
                    <>
                        <div className="yn-tabs-row">
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
                                leadingIcon={(label) =>
                                    label === 'Merged' ? (
                                        <Layers
                                            width={13}
                                            height={13}
                                            style={{ color: 'var(--yn-text-3)', flexShrink: 0 }}
                                        />
                                    ) : undefined
                                }
                            />
                        </div>
                        <div className="nb-toolbar">
                            <FamilyFilter
                                value={family}
                                onChange={(f) => updateParams({ [QP_FAMILY]: f === 'all' ? null : f })}
                            />
                        </div>
                        <div className="yn-content">
                            <NeighbourTable
                                rows={visibleRows}
                                totalCount={allRows.length}
                                searchActive={search.trim().length > 0 || family !== 'all'}
                                readOnlyNote={
                                    isMergedView ? (
                                        <span className="nb-footer-note">
                                            Merged is read-only — resolved by priority (lower value wins) across {tables.length} tables
                                        </span>
                                    ) : activeTableInfo?.name === 'kernel' ? (
                                        <span className="nb-footer-note">
                                            kernel is populated from netlink — manual edits are overwritten
                                        </span>
                                    ) : undefined
                                }
                                selectedIds={selectedIds}
                                sortState={sortState}
                                onSort={handleSort}
                                onRowClick={handleRowClick}
                                onEditRow={handleEditRow}
                                onSelectionChange={setSelectedIds}
                                emptyMessage={search || family !== 'all' ? 'No neighbours match the current filters.' : 'No neighbours.'}
                                canEditTable={canEditTable}
                                canDeleteTable={canDeleteTable}
                                onEditTable={() => setEditTableOpen(true)}
                                onDeleteTable={() => setDeleteTableOpen(true)}
                                onDeleteRow={isMergedView ? undefined : handleDeleteRowRequest}
                                canEditRow={!isMergedView}
                                isMergedView={isMergedView}
                                cache={cache}
                                tables={tables}
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

                <NeighbourPanel
                    open={panel.open}
                    mode={panel.mode}
                    neighbour={panel.neighbour}
                    tables={tables}
                    defaultTable={defaultAddTable}
                    activeTable={activeTab}
                    isMergedView={isMergedView}
                    cache={cache}
                    onClose={handleClosePanel}
                    onSubmit={handleSubmitNeighbour}
                    onDeleteRequest={(n) => setRowDeleteConfirm({ open: true, neighbour: n })}
                    onPinAsStatic={handlePinAsStatic}
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
