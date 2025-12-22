import React, { useCallback, useMemo, useState } from 'react';
import { Box } from '@gravity-ui/uikit';
import { API } from '../api';
import { toaster } from '../utils';
import type { Route } from '../api/routes';
import { PageLayout, PageLoader, EmptyState } from '../components';
import { parseCIDRPrefix, parseIPAddress, CIDRParseError, IPParseError } from '../utils';
import {
    type AddRouteFormData,
    type ConfigRoutesData,
    getRouteId,
    validatePrefix,
    validateNexthop,
    RoutePageHeader,
    DeleteRouteDialog,
    AddRouteDialog,
    RouteConfigContent,
    VirtualizedRouteTable,
    useMockMode,
    useRouteData,
} from './route';
import './route/route.css';

const RoutePage: React.FC = () => {
    const {
        configs,
        configRoutes,
        selectedRoutes,
        loading,
        activeConfigTab,
        setConfigs,
        setConfigRoutes,
        setSelectedRoutes,
        setActiveConfigTab,
        handleSelectionChange,
        handleConfigTabChange,
        reloadRoutes,
    } = useRouteData();

    const {
        mockEnabled,
        mockSize,
        mockGenerator,
        mockSelectedIds,
        setMockSelectedIds,
        handleMockToggle,
        handleMockSizeChange,
    } = useMockMode();

    const [deleteDialogOpen, setDeleteDialogOpen] = useState<boolean>(false);
    const [addDialogOpen, setAddDialogOpen] = useState<boolean>(false);
    const [addRouteForm, setAddRouteForm] = useState<AddRouteFormData>({
        configName: '',
        prefix: '',
        nexthopAddr: '',
        doFlush: false,
    });

    // Derived state
    const currentSelected = selectedRoutes.get(activeConfigTab);
    const isDeleteDisabled = !currentSelected || currentSelected.size === 0;
    const isFlushDisabled = !activeConfigTab;

    const handleAddRouteClick = useCallback((): void => {
        setAddRouteForm({
            configName: activeConfigTab,
            prefix: '',
            nexthopAddr: '',
            doFlush: false,
        });
        setAddDialogOpen(true);
    }, [activeConfigTab]);

    const handleDeleteRouteClick = useCallback((): void => {
        if (isDeleteDisabled) {
            toaster.warning('delete-route-warning', 'Please select routes to delete');
            return;
        }
        setDeleteDialogOpen(true);
    }, [isDeleteDisabled]);

    const handleFlushClick = useCallback(async (): Promise<void> => {
        if (!activeConfigTab) {
            toaster.warning('flush-route-config-warning', 'Please select a config to flush');
            return;
        }

        try {
            await API.route.flushRoutes({
                name: activeConfigTab,
            });
            toaster.success('flush-route-success', `Flushed routes for ${activeConfigTab}`);
        } catch (err) {
            toaster.error('flush-route-error', 'Failed to flush routes', err);
        }
    }, [activeConfigTab]);

    const handleAddRouteConfirm = useCallback(async (): Promise<void> => {
        const configName = addRouteForm.configName.trim();

        if (!configName) {
            toaster.error('add-route-config-error', 'Please enter a config name');
            return;
        }

        if (!addRouteForm.prefix || !addRouteForm.nexthopAddr) {
            toaster.error('add-route-validation-error', 'Please fill in all required fields');
            return;
        }

        const prefixResult = parseCIDRPrefix(addRouteForm.prefix);
        if (!prefixResult.ok) {
            let errorMessage = 'Invalid prefix format';
            switch (prefixResult.error) {
                case CIDRParseError.InvalidFormat:
                    errorMessage = 'Invalid prefix format. Use CIDR notation (e.g., 192.168.1.0/24 or 2001:db8::/32)';
                    break;
                case CIDRParseError.InvalidPrefixLength:
                    errorMessage = 'Invalid prefix length';
                    break;
                case CIDRParseError.InvalidIPAddress:
                    errorMessage = 'Invalid IP address in prefix';
                    break;
            }
            toaster.error('add-route-prefix-error', errorMessage);
            return;
        }

        const prefixLength = prefixResult.value.prefixLength;
        if (prefixLength === null) {
            toaster.error('add-route-prefix-error', 'Invalid prefix length');
            return;
        }

        const nexthopResult = parseIPAddress(addRouteForm.nexthopAddr);
        if (!nexthopResult.ok) {
            let errorMessage = 'Invalid nexthop address format';
            if (nexthopResult.error === IPParseError.InvalidFormat) {
                errorMessage = 'Invalid nexthop address format. Use valid IPv4 (e.g., 192.168.1.1) or IPv6 (e.g., 2001:db8::1) address';
            }
            toaster.error('add-route-nexthop-error', errorMessage);
            return;
        }

        try {
            await API.route.insertRoute({
                name: configName,
                prefix: addRouteForm.prefix,
                nexthopAddr: addRouteForm.nexthopAddr,
                doFlush: addRouteForm.doFlush,
            });

            setAddDialogOpen(false);

            const isNewConfig = !configs.includes(configName);
            const updatedConfigsList = isNewConfig
                ? [...configs, configName]
                : configs;

            if (isNewConfig) {
                setConfigs(updatedConfigsList);
                setActiveConfigTab(configName);
            }

            const reloadedRoutes = await reloadRoutes(updatedConfigsList);
            setConfigRoutes(reloadedRoutes);

            toaster.success('add-route-success', 'Route added successfully');
        } catch (err) {
            toaster.error('add-route-error', 'Failed to add route', err);
        }
    }, [addRouteForm, configs, reloadRoutes, setConfigs, setConfigRoutes, setActiveConfigTab]);

    const handleDeleteConfirm = useCallback(async (): Promise<void> => {
        const selected = currentSelected;

        if (!selected || selected.size === 0) {
            setDeleteDialogOpen(false);
            return;
        }

        const routes = configRoutes.get(activeConfigTab) || [];
        const selectedRoutesList = routes.filter((route: Route) => selected.has(getRouteId(route)));

        if (selectedRoutesList.length === 0) {
            setDeleteDialogOpen(false);
            return;
        }

        try {
            let skippedInvalidRoute = false;

            for (const route of selectedRoutesList) {
                if (!route.prefix || !route.nextHop) {
                    skippedInvalidRoute = true;
                    continue;
                }

                await API.route.deleteRoute({
                    name: activeConfigTab,
                    prefix: route.prefix,
                    nexthopAddr: route.nextHop,
                    doFlush: true,
                });
            }

            if (skippedInvalidRoute) {
                toaster.warning('delete-route-skip-warning', 'Skipped routes without prefix or nexthop address');
            }

            const reloadedRoutes = await reloadRoutes(configs);
            setConfigRoutes(reloadedRoutes);

            setSelectedRoutes((prev) => {
                const newSelected = new Map(prev);
                newSelected.set(activeConfigTab, new Set<string>());
                return newSelected;
            });

            setDeleteDialogOpen(false);

            toaster.success('delete-route-success', `Deleted ${selectedRoutesList.length} route(s)`);
        } catch (err) {
            toaster.error('delete-route-error', 'Failed to delete routes', err);
        }
    }, [currentSelected, activeConfigTab, configs, configRoutes, reloadRoutes, setConfigRoutes, setSelectedRoutes]);

    // Compute selected routes for delete dialog
    const selectedRoutesForDialog = useMemo((): Route[] => {
        if (!currentSelected || currentSelected.size === 0) return [];
        const routes = configRoutes.get(activeConfigTab) || [];
        return routes.filter((route: Route) => currentSelected.has(getRouteId(route)));
    }, [currentSelected, activeConfigTab, configRoutes]);

    const headerContent = (
        <RoutePageHeader
            onAddRoute={handleAddRouteClick}
            onDeleteRoute={handleDeleteRouteClick}
            onFlush={handleFlushClick}
            isDeleteDisabled={mockEnabled ? mockSelectedIds.size === 0 : isDeleteDisabled}
            isFlushDisabled={isFlushDisabled}
            mockEnabled={mockEnabled}
            onMockToggle={handleMockToggle}
            mockSize={mockSize}
            onMockSizeChange={handleMockSizeChange}
        />
    );

    if (loading && !mockEnabled) {
        return (
            <PageLayout title="Route">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (mockEnabled && mockGenerator) {
        return (
            <PageLayout header={headerContent}>
                <Box className="route-page__content route-page__content--with-generator">
                    <VirtualizedRouteTable
                        generator={mockGenerator}
                        selectedIds={mockSelectedIds}
                        onSelectionChange={(ids) => setMockSelectedIds(new Set(ids))}
                        getRouteId={getRouteId}
                    />
                </Box>
            </PageLayout>
        );
    }

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box className="route-page__empty">
                    <EmptyState message="No configs found. Use 'Add Route' to create a new configuration." />
                </Box>

                <AddRouteDialog
                    open={addDialogOpen}
                    onClose={() => setAddDialogOpen(false)}
                    onConfirm={handleAddRouteConfirm}
                    form={addRouteForm}
                    onFormChange={setAddRouteForm}
                    validatePrefix={validatePrefix}
                    validateNexthop={validateNexthop}
                />
            </PageLayout>
        );
    }

    const getRoutesData = (configName: string): ConfigRoutesData => {
        const routes = configRoutes.get(configName) || [];
        const selectedSet = selectedRoutes.get(configName) || new Set<string>();
        return {
            routes,
            selectedIds: Array.from(selectedSet),
        };
    };

    return (
        <PageLayout header={headerContent}>
            <Box className="route-page__content">
                <RouteConfigContent
                    configs={configs}
                    activeConfig={activeConfigTab}
                    onConfigChange={handleConfigTabChange}
                    getRoutesData={getRoutesData}
                    onSelectionChange={handleSelectionChange}
                    getRouteId={getRouteId}
                />
            </Box>

            <DeleteRouteDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDeleteConfirm}
                selectedRoutes={selectedRoutesForDialog}
            />

            <AddRouteDialog
                open={addDialogOpen}
                onClose={() => setAddDialogOpen(false)}
                onConfirm={handleAddRouteConfirm}
                form={addRouteForm}
                onFormChange={setAddRouteForm}
                validatePrefix={validatePrefix}
                validateNexthop={validateNexthop}
            />
        </PageLayout>
    );
};

export default RoutePage;
