import React, { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers } from '../../../hooks';
import { Button, Icon, Text } from '@gravity-ui/uikit';
import { Plus, Layers } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { BulkDeleteModal, DeleteConfigModal } from '../../../components';
import { CommandPalette, CommandPaletteTrigger, usePaletteShortcut } from '../../_shared/command-palette';
import type { Command, RowAdapter } from '../../_shared/command-palette';
import { stringToIPAddress, ipAddressToString } from '../../../utils/netip';
import { parseIPAddress } from '../../../utils';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { NeighbourTable } from './NeighbourTable';
import NeighbourPanel from './NeighbourPanel';
import TableModal from './TableModal';
import { useNeighbours } from './useNeighbours';
import { getNeighbourId, isSortableColumn, isSortDirection, sortComparators } from './utils';
import { MERGED_TAB, DEFAULT_SORT } from './types';
import type { SortState, SortableColumn } from './types';
import { FamilyFilter, type IPFamily } from '../../../components/VirtualTable';
import { nudStateToName, STATE_META } from './stateMeta';
import '../../../styles/draft-page.scss';
import '../../_shared/command-palette/command-palette.scss';

const QP_TAB = 'tab';
const QP_SORT = 'sort';
const QP_ORDER = 'order';
const QP_FAMILY = 'family';
const QP_STATE = 'state';

const parseFamily = (params: URLSearchParams): IPFamily => {
    const v = params.get(QP_FAMILY);
    if (v === 'v4' || v === 'v6') return v;
    return 'all';
};

const parseStateFilter = (params: URLSearchParams): string | null => {
    const v = params.get(QP_STATE);
    return v || null;
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

/** Neighbours page — shows neighbour tables and entries with inline panel editing. */
const NeighboursPage: React.FC = () => {
    const [searchParams, setSearchParams] = useSearchParams();

    const activeTab = parseTab(searchParams);
    const sortState = parseSortState(searchParams);
    const family = parseFamily(searchParams);
    const stateFilter = parseStateFilter(searchParams);

    const [paused, setPaused] = useState(false);
    const [paletteOpen, setPaletteOpen] = useState(false);
    const [flashRowId, setFlashRowId] = useState<string | null>(null);

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
        refreshNow,
    } = useNeighbours(activeTab, paused);

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

    usePaletteShortcut(paletteOpen, setPaletteOpen);

    const { updateParams } = useSearchParamHelpers(setSearchParams);

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
        if (stateFilter) {
            res = res.filter((n) => nudStateToName(n.state) === stateFilter);
        }
        if (sortState.column) {
            const cmp = sortComparators[sortState.column];
            res = [...res].sort(sortState.direction === 'desc' ? (a, b) => cmp(b, a) : cmp);
        }
        return res;
    }, [allRows, sortState, family, stateFilter]);

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

    const handleJumpToRow = useCallback((id: string): void => {
        const inVisible = visibleRows.some((n) => getNeighbourId(n) === id);
        if (!inVisible) {
            updateParams({ [QP_FAMILY]: null, [QP_STATE]: null });
        }
        setFlashRowId(null);
        setTimeout(() => setFlashRowId(id), 0);
    }, [visibleRows, updateParams]);

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

    const distinctStates = useMemo((): string[] => {
        const seen = new Set<string>();
        for (const n of allRows) {
            seen.add(nudStateToName(n.state));
        }
        return Array.from(seen).sort();
    }, [allRows]);

    const stateCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        for (const n of allRows) {
            const name = nudStateToName(n.state);
            m.set(name, (m.get(name) ?? 0) + 1);
        }
        return m;
    }, [allRows]);

    const neighbourCommands = useMemo((): Command[] => {
        const cmds: Command[] = [];

        if (tables.length > 0) {
            cmds.push({
                id: '__add_neighbour',
                icon: '+',
                label: 'Add neighbour',
                sub: 'Open the add-neighbour panel',
                keywords: 'add neighbour create new',
                onSelect: () => { openAdd(); setPaletteOpen(false); },
            });
        }

        cmds.push({
            id: '__add_table',
            icon: '⊞',
            label: 'Add table',
            sub: 'Create a new neighbour table',
            keywords: 'add table create new',
            onSelect: () => { setCreateTableOpen(true); setPaletteOpen(false); },
        });

        if (canEditTable) {
            cmds.push({
                id: '__edit_table',
                icon: '✎',
                label: 'Edit current table',
                sub: activeTableInfo?.name,
                keywords: 'edit table settings priority',
                onSelect: () => { setEditTableOpen(true); setPaletteOpen(false); },
            });
        }

        if (canDeleteTable) {
            cmds.push({
                id: '__delete_table',
                icon: '✕',
                label: 'Delete current table',
                sub: activeTableInfo?.name,
                keywords: 'delete remove table',
                onSelect: () => { setDeleteTableOpen(true); setPaletteOpen(false); },
            });
        }

        if (!isMergedView) {
            cmds.push({
                id: '__switch_merged',
                icon: '⊕',
                label: 'Switch to Merged view',
                keywords: 'merged view all tables',
                onSelect: () => { handleTabSelect(MERGED_TAB); setPaletteOpen(false); },
            });
        }

        for (const t of tables) {
            if (!t.name || t.name === activeTab) continue;
            const name = t.name;
            cmds.push({
                id: `__tab_${name}`,
                icon: '⇥',
                label: `Switch to table ${name}`,
                keywords: `switch table ${name}`,
                onSelect: () => { handleTabSelect(name); setPaletteOpen(false); },
            });
        }

        cmds.push({
            id: '__filter_v4',
            icon: '4',
            label: 'Filter IPv4 only',
            keywords: 'ipv4 filter family',
            onSelect: () => { updateParams({ [QP_FAMILY]: 'v4' }); setPaletteOpen(false); },
        });

        cmds.push({
            id: '__filter_v6',
            icon: '6',
            label: 'Filter IPv6 only',
            keywords: 'ipv6 filter family',
            onSelect: () => { updateParams({ [QP_FAMILY]: 'v6' }); setPaletteOpen(false); },
        });

        cmds.push({
            id: '__clear_filters',
            icon: '✕',
            label: 'Clear filters',
            keywords: 'clear reset filters all',
            onSelect: () => { updateParams({ [QP_FAMILY]: null, [QP_STATE]: null }); setPaletteOpen(false); },
        });

        for (const stateName of distinctStates) {
            const sn = stateName;
            cmds.push({
                id: `__filter_state_${sn}`,
                icon: '◉',
                label: `Filter state: ${sn}`,
                keywords: `filter state nud ${sn.toLowerCase()}`,
                onSelect: () => { updateParams({ [QP_STATE]: sn }); setPaletteOpen(false); },
            });
        }

        if (stateFilter) {
            cmds.push({
                id: '__clear_state_filter',
                icon: '✕',
                label: 'Clear state filter',
                keywords: 'clear state filter nud',
                onSelect: () => { updateParams({ [QP_STATE]: null }); setPaletteOpen(false); },
            });
        }

        cmds.push({
            id: '__refresh_now',
            icon: '⟳',
            label: 'Refresh now',
            keywords: 'refresh reload update now',
            onSelect: () => { refreshNow().catch(() => {}); setPaletteOpen(false); },
        });

        cmds.push({
            id: '__toggle_autorefresh',
            icon: paused ? '▶' : '⏸',
            label: paused ? 'Resume auto-refresh' : 'Pause auto-refresh',
            keywords: 'pause resume auto refresh toggle',
            onSelect: () => { setPaused((v) => !v); setPaletteOpen(false); },
        });

        if (selectedIds.size > 0 && !isMergedView) {
            cmds.push({
                id: '__bulk_delete',
                icon: '✕',
                label: 'Delete selected neighbours',
                sub: `${selectedIds.size} selected`,
                keywords: 'delete remove selected bulk',
                onSelect: () => { setBulkRemoveOpen(true); setPaletteOpen(false); },
            });

            cmds.push({
                id: '__clear_selection',
                icon: '☐',
                label: 'Clear selection',
                keywords: 'clear deselect selection',
                onSelect: () => { setSelectedIds(new Set()); setPaletteOpen(false); },
            });
        }

        return cmds;
    }, [
        openAdd,
        canEditTable,
        canDeleteTable,
        activeTableInfo,
        isMergedView,
        tables,
        activeTab,
        handleTabSelect,
        updateParams,
        distinctStates,
        stateFilter,
        refreshNow,
        paused,
        selectedIds,
    ]);

    const neighbourDynamicCommands = useCallback((q: string): Command[] => {
        if (!parseIPAddress(q.trim()).ok) return [];
        const ip = q.trim();
        const existing = allRows.find((n) => ipAddressToString(n.next_hop) === ip);
        if (existing) {
            const id = getNeighbourId(existing);
            return [
                {
                    id: '__jump_ip',
                    icon: '⌖',
                    label: `Jump to ${ip}`,
                    sub: 'Scroll to this neighbour in the table',
                    onSelect: () => { handleJumpToRow(id); setPaletteOpen(false); },
                },
            ];
        }
        if (tables.length === 0) return [];
        return [
            {
                id: '__add_ip',
                icon: '+',
                label: `Add neighbour ${ip}`,
                sub: 'Open the add panel pre-filled with this IP',
                onSelect: () => {
                    const wire = stringToIPAddress(ip);
                    if (wire) {
                        setPanel({ open: true, mode: 'add', neighbour: { next_hop: wire } });
                    }
                    setPaletteOpen(false);
                },
            },
        ];
    }, [allRows, handleJumpToRow, tables.length]);

    const neighbourRowAdapter: RowAdapter<Neighbour> = {
        rows: allRows,
        getId: getNeighbourId,
        getLabel: (n) => ipAddressToString(n.next_hop) || '—',
        getSub: (n) => [n.device, n.source, nudStateToName(n.state)].filter(Boolean).join(' · '),
        searchText: (n) => ipAddressToString(n.next_hop) + ' ' + (n.device || '') + ' ' + (n.source || ''),
        onSelect: (id) => { handleJumpToRow(id); setPaletteOpen(false); },
        icon: '→',
        max: 7,
    };

    const searchActive = family !== 'all' || !!stateFilter;

    const pageHeader = (
        <div className="page-header-bar">
            <Text variant="header-1">Neighbours</Text>
            <CommandPaletteTrigger placeholder="Search neighbours or type an IP…" onOpen={() => setPaletteOpen(true)} />
            <div className="page-header-bar__actions">
                <Button view="outlined" onClick={() => setCreateTableOpen(true)}>
                    <Icon data={Plus} size={16} />
                    Add Table
                </Button>
                <Button view="action" onClick={openAdd} disabled={tables.length === 0}>
                    <Icon data={Plus} size={16} />
                    Add Neighbour
                </Button>
            </div>
        </div>
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
                            {distinctStates.length > 0 && (
                                <>
                                    <div className="nb-toolbar-sep" />
                                    {distinctStates.map((stateName) => {
                                        const isActive = stateFilter === stateName;
                                        const color = STATE_META[stateName]?.color ?? STATE_META['UNKNOWN'].color;
                                        const count = stateCounts.get(stateName) ?? 0;
                                        return (
                                            <button
                                                key={stateName}
                                                type="button"
                                                className={`nb-state-chip${isActive ? ' nb-state-chip--active' : ''}`}
                                                style={{ '--chip-color': color } as React.CSSProperties}
                                                onClick={() => updateParams({ [QP_STATE]: isActive ? null : stateName })}
                                                title={isActive ? `Clear state filter (${stateName})` : `Filter by state: ${stateName}`}
                                            >
                                                {stateName}
                                                <span className="nb-state-chip__badge">{count}</span>
                                            </button>
                                        );
                                    })}
                                </>
                            )}
                        </div>
                        <div className="yn-content">
                            <NeighbourTable
                                rows={visibleRows}
                                totalCount={allRows.length}
                                searchActive={searchActive}
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
                                emptyMessage={searchActive ? 'No neighbours match the current filters.' : 'No neighbours.'}
                                canEditTable={canEditTable}
                                canDeleteTable={canDeleteTable}
                                onEditTable={() => setEditTableOpen(true)}
                                onDeleteTable={() => setDeleteTableOpen(true)}
                                onDeleteRow={isMergedView ? undefined : handleDeleteRowRequest}
                                canEditRow={!isMergedView}
                                isMergedView={isMergedView}
                                cache={cache}
                                tables={tables}
                                flashRowId={flashRowId}
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
                    noun="table"
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

                <TableModal
                    mode="create"
                    open={createTableOpen}
                    onClose={() => setCreateTableOpen(false)}
                    onCreate={handleCreateTable}
                    existingNames={tables.map((t) => t.name || '')}
                />

                <TableModal
                    mode="edit"
                    open={editTableOpen}
                    onClose={() => setEditTableOpen(false)}
                    onSave={handleEditTable}
                    tableInfo={activeTableInfo}
                />

                <CommandPalette<Neighbour>
                    open={paletteOpen}
                    onClose={() => setPaletteOpen(false)}
                    placeholder="Search neighbours or type an IP…"
                    commands={neighbourCommands}
                    dynamicCommands={neighbourDynamicCommands}
                    rowAdapter={neighbourRowAdapter}
                />
            </div>
        </PageLayout>
    );
};

export default NeighboursPage;
