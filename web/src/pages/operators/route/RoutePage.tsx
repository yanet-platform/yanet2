import React, { useCallback, useMemo, useState } from 'react';
import { Button, Icon, Text } from '@gravity-ui/uikit';
import { ArrowRightToLine, Funnel, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder } from '../../../components';
import { AddConfigModal } from '../../_shared/draft';
import { BulkDeleteModal } from '../../../components';
import { CommandPalette, CommandPaletteTrigger, usePaletteShortcut } from '../../_shared/command-palette';
import type { Command, RowAdapter } from '../../_shared/command-palette';
import { API } from '../../../api';
import { toaster, parseIPAddress } from '../../../utils';
import { stringToIPAddress, ipAddressToString } from '../../../utils/netip';
import { RouteSourceID, type Route } from '../../../api/routes';
import { useRIB } from './useRIB';
import { RIBTable } from './RIBTable';
import RouteDrawer from './RouteDrawer';
import LookupDrawer from './LookupDrawer';
import { getRouteId, sortComparators, planRouteSubmit, groupByPrefix, filterByFamily } from './utils';
import type { RouteSortState, RouteSortableColumn, IPFamily } from './types';
import { FamilyFilter } from '../../../components/VirtualTable';
import '../../../styles/draft-page.scss';
import './route.scss';

const RoutePage: React.FC = () => {
    const { configs, configRoutes, selectedIds, loading, refreshing, reload, addLocalConfig, setSelected } = useRIB();

    const [activeConfig, setActiveConfig] = useState('');
    const [search, setSearch] = useState('');
    const [sortState, setSortState] = useState<RouteSortState>({ column: null, direction: 'asc' });
    const [drawer, setDrawer] = useState<{ open: boolean; mode: 'add' | 'edit'; route: Route | null }>({
        open: false,
        mode: 'add',
        route: null,
    });
    const [bulkDeleteOpen, setBulkDeleteOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [rowDeleteConfirm, setRowDeleteConfirm] = useState<{ open: boolean; route: Route | null }>({
        open: false,
        route: null,
    });

    const [paletteOpen, setPaletteOpen] = useState(false);
    const [lookupOpen, setLookupOpen] = useState(false);
    const [lookupInitialQuery, setLookupInitialQuery] = useState('');
    const [family, setFamily] = useState<IPFamily>('all');
    const [bestOnly, setBestOnly] = useState(false);
    const [conflictsOnly, setConflictsOnly] = useState(false);
    const [flashRowId, setFlashRowId] = useState<string | null>(null);

    const currentConfig = activeConfig || configs[0] || '';
    const allRows = configRoutes.get(currentConfig) || [];
    const currentSelected = selectedIds.get(currentConfig) || new Set<string>();

    const conflictMap = useMemo((): Map<string, number> => {
        const groups = groupByPrefix(allRows);
        const m = new Map<string, number>();
        groups.forEach((group, prefix) => m.set(prefix, group.length));
        return m;
    }, [allRows]);

    const conflictCount = useMemo((): number => {
        let count = 0;
        conflictMap.forEach((n) => { if (n > 1) count++; });
        return count;
    }, [conflictMap]);

    const visibleRows = useMemo(() => {
        let res = allRows;

        res = filterByFamily(res, family);

        if (bestOnly) {
            res = res.filter((r) => r.is_best === true);
        }

        if (conflictsOnly) {
            res = res.filter((r) => (conflictMap.get(r.prefix || '') ?? 1) > 1);
        }

        const q = search.trim().toLowerCase();
        if (q) {
            res = res.filter((r) =>
                (r.prefix || '').toLowerCase().includes(q) ||
                ipAddressToString(r.next_hop).toLowerCase().includes(q) ||
                ipAddressToString(r.peer).toLowerCase().includes(q)
            );
        }
        if (sortState.column) {
            const cmp = sortComparators[sortState.column];
            res = [...res].sort(sortState.direction === 'desc' ? (a, b) => cmp(b, a) : cmp);
        }
        return res;
    }, [allRows, search, sortState, family, bestOnly, conflictsOnly, conflictMap]);

    const counts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        configs.forEach((c) => m.set(c, (configRoutes.get(c) || []).length));
        return m;
    }, [configs, configRoutes]);

    usePaletteShortcut(paletteOpen, setPaletteOpen);

    const handleSort = useCallback((col: RouteSortableColumn): void => {
        setSortState((prev) => {
            if (prev.column !== col) {
                return { column: col, direction: 'asc' };
            }
            if (prev.direction === 'asc') {
                return { column: col, direction: 'desc' };
            }
            return { column: null, direction: 'asc' };
        });
    }, []);

    const openAdd = useCallback((): void => {
        setDrawer({ open: true, mode: 'add', route: null });
    }, []);

    const handleEditRow = useCallback((id: string): void => {
        const route = allRows.find((r) => getRouteId(r) === id) || null;
        setDrawer({ open: true, mode: 'edit', route });
    }, [allRows]);

    const handleCloseDrawer = useCallback((): void => {
        setDrawer((prev) => ({ ...prev, open: false }));
    }, []);

    const handleSubmitRoute = useCallback(async (params: { prefix: string; nexthopAddr: string; doFlush: boolean }): Promise<void> => {
        const nexthopIp = stringToIPAddress(params.nexthopAddr);
        if (!nexthopIp) {
            toaster.error('route-nexthop-error', 'Invalid next-hop address');
            return;
        }

        const isEdit = drawer.mode === 'edit';
        const original = drawer.route;
        const newNexthopStr = ipAddressToString(nexthopIp);
        const originalNexthopStr = ipAddressToString(original?.next_hop);
        const ops = planRouteSubmit(
            drawer.mode,
            { prefix: params.prefix, nexthopIp, doFlush: params.doFlush },
            newNexthopStr,
            original,
            originalNexthopStr,
        );

        try {
            for (const op of ops) {
                if (op.type === 'delete') {
                    await API.route.deleteRoute({
                        name: currentConfig,
                        prefix: op.prefix,
                        nexthop_addr: op.nexthop,
                        do_flush: false,
                        source_id: RouteSourceID.STATIC,
                    });
                } else {
                    await API.route.insertRoute({
                        name: currentConfig,
                        prefix: op.prefix,
                        nexthop_addr: op.nexthop,
                        do_flush: op.doFlush,
                        source_id: RouteSourceID.STATIC,
                    });
                }
            }

            await reload();
            toaster.success('route-add-success', isEdit ? 'Route updated.' : 'Route added.');
        } catch (err) {
            toaster.error('route-add-error', isEdit ? 'Failed to update route' : 'Failed to add route', err);
            throw err;
        }
    }, [currentConfig, reload, drawer.mode, drawer.route]);

    const handleDeleteRoute = useCallback(async (route: Route): Promise<void> => {
        if (!route.prefix || !route.next_hop) {
            toaster.warning('route-delete-invalid', 'Route has no prefix or next-hop');
            return;
        }
        try {
            await API.route.deleteRoute({
                name: currentConfig,
                prefix: route.prefix,
                nexthop_addr: route.next_hop,
                do_flush: true,
                source_id: RouteSourceID.STATIC,
            });
            await reload();
            setSelected(currentConfig, new Set());
            toaster.success('route-delete-success', 'Route deleted.');
        } catch (err) {
            toaster.error('route-delete-error', 'Failed to delete route', err);
            throw err;
        }
    }, [currentConfig, reload, setSelected]);

    const handleDeleteRowRequest = useCallback((id: string): void => {
        const route = allRows.find((r) => getRouteId(r) === id) || null;
        if (route) setRowDeleteConfirm({ open: true, route });
    }, [allRows]);

    const handleDeleteRowConfirm = useCallback(async (): Promise<void> => {
        const route = rowDeleteConfirm.route;
        setRowDeleteConfirm({ open: false, route: null });
        if (!route) return;
        await handleDeleteRoute(route);
    }, [rowDeleteConfirm.route, handleDeleteRoute]);

    const handleFlush = useCallback(async (): Promise<void> => {
        if (!currentConfig) return;
        try {
            await API.route.flushRoutes({ name: currentConfig });
            toaster.success('flush-success', `Flushed routes for ${currentConfig}.`);
        } catch (err) {
            toaster.error('flush-error', 'Failed to flush routes', err);
        }
    }, [currentConfig]);

    const handleBulkDelete = useCallback(async (): Promise<void> => {
        const routes = allRows.filter((r) => currentSelected.has(getRouteId(r)));
        let skipped = 0;
        let deleted = 0;
        for (const route of routes) {
            if (!route.prefix || !route.next_hop) {
                skipped++;
                continue;
            }
            try {
                await API.route.deleteRoute({
                    name: currentConfig,
                    prefix: route.prefix,
                    nexthop_addr: route.next_hop,
                    do_flush: true,
                    source_id: RouteSourceID.STATIC,
                });
                deleted++;
            } catch (err) {
                toaster.error('bulk-delete-error', `Failed to delete route ${route.prefix}`, err);
            }
        }
        await reload();
        setSelected(currentConfig, new Set());
        setBulkDeleteOpen(false);
        if (deleted > 0) {
            toaster.success('bulk-delete-success', `Deleted ${deleted} route${deleted !== 1 ? 's' : ''}.`);
        }
        if (skipped > 0) {
            toaster.warning('bulk-delete-skip', `Skipped ${skipped} route${skipped !== 1 ? 's' : ''} without prefix or nexthop.`);
        }
    }, [allRows, currentSelected, currentConfig, reload, setSelected]);

    const handleLookupIP = useCallback((ip: string): void => {
        setLookupInitialQuery(ip);
        setLookupOpen(true);
    }, []);

    const handleClearFilters = useCallback((): void => {
        setFamily('all');
        setBestOnly(false);
        setConflictsOnly(false);
        setSearch('');
    }, []);

    const handleShowInTable = useCallback((prefix: string): void => {
        handleClearFilters();
        const matchedRow = allRows.find((r) => r.prefix === prefix);
        if (matchedRow) {
            const id = getRouteId(matchedRow);
            setFlashRowId(null);
            setTimeout(() => setFlashRowId(id), 0);
        }
    }, [allRows, handleClearFilters]);

    const handleJumpToRow = useCallback((id: string): void => {
        setFlashRowId(null);
        setTimeout(() => setFlashRowId(id), 0);
    }, []);

    const routeCommands: Command[] = [
        {
            id: '__add',
            icon: '+',
            label: 'Add route',
            sub: 'Open the add-route drawer',
            keywords: 'add route insert',
            onSelect: () => { openAdd(); setPaletteOpen(false); },
        },
        {
            id: '__flush',
            icon: '⟳',
            label: 'Flush RIB → FIB',
            sub: 'Push best routes to the dataplane',
            keywords: 'flush rib fib push',
            onSelect: () => { handleFlush(); setPaletteOpen(false); },
        },
        {
            id: '__open_lookup',
            icon: '⌖',
            label: 'Open Route Lookup',
            sub: 'Look up an IP address in the RIB',
            keywords: 'route lookup ip search',
            onSelect: () => { setLookupInitialQuery(''); setLookupOpen(true); setPaletteOpen(false); },
        },
        {
            id: '__best_only',
            icon: '★',
            label: 'Show best paths only',
            keywords: 'best paths only filter',
            onSelect: () => { setBestOnly((v) => !v); setPaletteOpen(false); },
        },
        {
            id: '__conflicts',
            icon: '⚡',
            label: 'Show conflicts only',
            sub: 'Prefixes with more than one candidate route',
            keywords: 'conflicts duplicate prefix',
            onSelect: () => { setConflictsOnly((v) => !v); setPaletteOpen(false); },
        },
        {
            id: '__v6',
            icon: '6',
            label: 'Filter IPv6 only',
            keywords: 'ipv6 filter family',
            onSelect: () => { setFamily('v6'); setPaletteOpen(false); },
        },
        {
            id: '__v4',
            icon: '4',
            label: 'Filter IPv4 only',
            keywords: 'ipv4 filter family',
            onSelect: () => { setFamily('v4'); setPaletteOpen(false); },
        },
        {
            id: '__clear',
            icon: '✕',
            label: 'Clear filters',
            keywords: 'clear reset filters all',
            onSelect: () => { handleClearFilters(); setPaletteOpen(false); },
        },
    ];

    const routeDynamicCommands = useCallback((q: string): Command[] => {
        if (!parseIPAddress(q.trim()).ok) return [];
        const ip = q.trim();
        return [
            {
                id: '__lookup_ip',
                icon: '⌖',
                label: `Look up ${ip}`,
                sub: 'Route Lookup — longest prefix match',
                onSelect: () => { handleLookupIP(ip); setPaletteOpen(false); },
            },
        ];
    }, [handleLookupIP]);

    const routeRowAdapter: RowAdapter<Route> = {
        rows: allRows,
        getId: getRouteId,
        getLabel: (r) => r.prefix || '(no prefix)',
        getSub: (r) => ipAddressToString(r.next_hop) || '—',
        searchText: (r) => (r.prefix || '') + ' ' + ipAddressToString(r.next_hop),
        onSelect: (id) => { handleJumpToRow(id); setPaletteOpen(false); },
        icon: '→',
        max: 7,
    };

    const pageHeader = (
        <div className="page-header-bar">
            <Text variant="header-1">Routing Table</Text>
            <CommandPaletteTrigger placeholder="Search or look up an IP…" onOpen={() => setPaletteOpen(true)} />
            <div className="page-header-bar__actions">
                <Button view="outlined" onClick={handleFlush} disabled={!currentConfig}>
                    <Icon data={ArrowRightToLine} size={16} />
                    Flush RIB → FIB
                </Button>
                <Button view="action" onClick={openAdd} disabled={configs.length === 0}>
                    <Icon data={Plus} size={16} />
                    Add Route
                </Button>
            </div>
        </div>
    );

    if (loading && configs.length === 0) {
        return (
            <PageLayout header={pageHeader} className="ro-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="ro-layout">
            <div className="yn-page ro-page" aria-busy={refreshing}>
                {configs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No route configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={configs}
                            activeConfig={currentConfig}
                            counts={counts}
                            dirtyConfigs={new Set()}
                            onSelect={(c) => {
                                setActiveConfig(c);
                            }}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />
                        <div className="ro-toolbar">
                            <FamilyFilter value={family} onChange={setFamily} />
                            <button
                                type="button"
                                className={`ro-chip ro-chip--best${bestOnly ? ' ro-chip--active' : ''}`}
                                onClick={() => setBestOnly((v) => !v)}
                                title="Show only the best route for each prefix"
                            >
                                <svg width={13} height={13} viewBox="0 0 24 24" fill={bestOnly ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }}>
                                    <path d="M13 2 3 14h9l-1 8 10-12h-9l1-8Z" />
                                </svg>
                                Best only
                            </button>
                            <button
                                type="button"
                                className={`ro-chip ro-chip--conflict${conflictsOnly ? ' ro-chip--active' : ''}`}
                                onClick={() => setConflictsOnly((v) => !v)}
                                title="Show only prefixes with multiple candidate routes"
                            >
                                <svg width={13} height={13} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" style={{ display: 'block', flexShrink: 0 }}>
                                    <path d="M6 3v12" />
                                    <path d="M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
                                    <path d="M6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
                                    <path d="M15 6a9 9 0 0 1-9 9" />
                                </svg>
                                Conflicts
                                {conflictCount > 0 && (
                                    <span className="ro-chip__badge">{conflictCount}</span>
                                )}
                            </button>
                            <div style={{ flex: 1 }} />
                            <div style={{ flexBasis: 230, flexShrink: 1 }}>
                                <SearchInput
                                    value={search}
                                    onUpdate={setSearch}
                                    placeholder="Filter rows…"
                                    enableFocusShortcut={false}
                                    showShortcutHint={false}
                                    icon={Funnel}
                                />
                            </div>
                            <span className="ro-count">
                                <span style={{ color: 'var(--yn-text)', fontWeight: 600 }}>{visibleRows.length.toLocaleString()}</span>
                                {' / '}{allRows.length.toLocaleString()}
                            </span>
                        </div>
                        <div className="yn-content">
                            <RIBTable
                                rows={visibleRows}
                                selectedIds={currentSelected}
                                sortState={sortState}
                                onSort={handleSort}
                                onEditRow={handleEditRow}
                                onSelectionChange={(ids) => setSelected(currentConfig, ids)}
                                emptyMessage={search || family !== 'all' || bestOnly || conflictsOnly ? 'No routes match the current filters.' : 'No routes.'}
                                onDeleteRow={handleDeleteRowRequest}
                                conflictMap={conflictMap}
                                flashRowId={flashRowId}
                            />
                        </div>
                    </>
                )}

                {currentSelected.size > 0 && (
                    <BulkBar
                        count={currentSelected.size}
                        itemNoun="route"
                        onDelete={() => setBulkDeleteOpen(true)}
                        onClear={() => setSelected(currentConfig, new Set())}
                    />
                )}

                <BulkDeleteModal
                    open={bulkDeleteOpen}
                    count={currentSelected.size}
                    itemNoun="route"
                    configName={currentConfig}
                    onClose={() => setBulkDeleteOpen(false)}
                    onConfirm={handleBulkDelete}
                    immediate
                />

                <BulkDeleteModal
                    open={rowDeleteConfirm.open}
                    count={1}
                    itemNoun="route"
                    configName={currentConfig}
                    onClose={() => setRowDeleteConfirm({ open: false, route: null })}
                    onConfirm={handleDeleteRowConfirm}
                    immediate
                />

                <RouteDrawer
                    open={drawer.open}
                    mode={drawer.mode}
                    route={drawer.route}
                    configName={currentConfig}
                    onClose={handleCloseDrawer}
                    onSubmit={handleSubmitRoute}
                    onDelete={handleDeleteRoute}
                />

                <AddConfigModal
                    open={addConfigOpen}
                    onClose={() => setAddConfigOpen(false)}
                    onCreate={(name) => {
                        addLocalConfig(name);
                        setActiveConfig(name);
                        setAddConfigOpen(false);
                    }}
                    title="Add route config"
                    placeholder="e.g. route0"
                    existingNames={configs}
                />

                <CommandPalette<Route>
                    open={paletteOpen}
                    onClose={() => setPaletteOpen(false)}
                    placeholder="Search routes, prefixes, or type an IP…"
                    commands={routeCommands}
                    dynamicCommands={routeDynamicCommands}
                    rowAdapter={routeRowAdapter}
                />

                <LookupDrawer
                    open={lookupOpen}
                    configName={currentConfig}
                    initialQuery={lookupInitialQuery}
                    onClose={() => setLookupOpen(false)}
                    onShowInTable={handleShowInTable}
                />

            </div>
        </PageLayout>
    );
};

export default RoutePage;
