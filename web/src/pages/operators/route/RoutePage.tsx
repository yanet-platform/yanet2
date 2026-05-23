import React, { useCallback, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { AddConfigModal, BulkDeleteModal } from '../../_shared/draft';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import { stringToIPAddress, ipAddressToString } from '../../../utils/netip';
import { RouteSourceID, type Route } from '../../../api/routes';
import { useRIB } from './useRIB';
import { RIBTable } from './RIBTable';
import RouteDrawer from './RouteDrawer';
import { getRouteId, sortComparators } from './utils';
import type { RouteSortState, RouteSortableColumn } from './types';
import '../../../styles/draft-page.scss';

const RoutePage: React.FC = () => {
    const { configs, configRoutes, selectedIds, loading, reload, addLocalConfig, setSelected } = useRIB();

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
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);

    const searchRef = useRef<HTMLInputElement>(null);

    const currentConfig = activeConfig || configs[0] || '';
    const allRows = configRoutes.get(currentConfig) || [];
    const currentSelected = selectedIds.get(currentConfig) || new Set<string>();

    const visibleRows = useMemo(() => {
        let res = allRows;
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
    }, [allRows, search, sortState]);

    const counts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        configs.forEach((c) => m.set(c, (configRoutes.get(c) || []).length));
        return m;
    }, [configs, configRoutes]);

    const handleSort = useCallback((col: RouteSortableColumn): void => {
        setSortState((prev) => ({
            column: col,
            direction: prev.column === col && prev.direction === 'asc' ? 'desc' : 'asc',
        }));
    }, []);

    const openAdd = useCallback((): void => {
        setDrawer({ open: true, mode: 'add', route: null });
    }, []);

    const handleEditRow = useCallback((id: string): void => {
        const route = allRows.find((r) => getRouteId(r) === id) || null;
        setDrawer({ open: true, mode: 'edit', route });
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

    const handleSubmitRoute = useCallback(async (params: { prefix: string; nexthopAddr: string; doFlush: boolean }): Promise<void> => {
        const nexthopIp = stringToIPAddress(params.nexthopAddr);
        if (!nexthopIp) {
            toaster.error('route-nexthop-error', 'Invalid next-hop address');
            return;
        }

        const isEdit = drawer.mode === 'edit';
        const original = drawer.route;
        const originalPrefix = original?.prefix;
        const originalNexthop = original?.next_hop;
        const newNexthopStr = ipAddressToString(nexthopIp);
        const originalNexthopStr = ipAddressToString(originalNexthop);
        const keyChanged = isEdit && !!original && (originalPrefix !== params.prefix || originalNexthopStr !== newNexthopStr);

        try {
            if (keyChanged && originalPrefix && originalNexthop) {
                await API.route.deleteRoute({
                    name: currentConfig,
                    prefix: originalPrefix,
                    nexthop_addr: originalNexthop,
                    do_flush: false,
                    source_id: RouteSourceID.STATIC,
                });
            }

            await API.route.insertRoute({
                name: currentConfig,
                prefix: params.prefix,
                nexthop_addr: nexthopIp,
                do_flush: params.doFlush,
                source_id: RouteSourceID.STATIC,
            });

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

    const handleFlush = useCallback(async (): Promise<void> => {
        if (!currentConfig) return;
        try {
            await API.route.flushRoutes({ name: currentConfig });
            await reload();
            toaster.success('flush-success', `Flushed routes for ${currentConfig}.`);
        } catch (err) {
            toaster.error('flush-error', 'Failed to flush routes', err);
        }
    }, [currentConfig, reload]);

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

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Routing Table</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <TextInput
                    controlRef={searchRef as React.RefObject<HTMLInputElement | null>}
                    value={search}
                    onUpdate={setSearch}
                    placeholder="Search prefix, nexthop or peer… (/)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            <Button view="outlined" onClick={handleFlush} disabled={!currentConfig}>
                Flush RIB → FIB
            </Button>
            <Button view="action" onClick={openAdd} disabled={configs.length === 0}>
                <Icon data={Plus} size={16} />
                Add Route
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
                {configs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No route configurations found.</div>
                        <Button view="action" onClick={() => setAddConfigOpen(true)}>Add Config</Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={configs}
                            activeConfig={currentConfig}
                            counts={counts}
                            dirtyConfigs={new Set()}
                            onSelect={(c) => {
                                setActiveConfig(c);
                                setActiveRowId(null);
                                setEditingRowId(null);
                            }}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />
                        <div className="fw-content">
                            <RIBTable
                                rows={visibleRows}
                                selectedIds={currentSelected}
                                activeRowId={activeRowId}
                                editingRowId={editingRowId}
                                sortState={sortState}
                                onSort={handleSort}
                                onRowClick={handleRowClick}
                                onEditRow={handleEditRow}
                                onSelectionChange={(ids) => setSelected(currentConfig, ids)}
                                emptyMessage={search ? 'No routes match your search.' : 'No routes.'}
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
            </div>
        </PageLayout>
    );
};

export default RoutePage;
