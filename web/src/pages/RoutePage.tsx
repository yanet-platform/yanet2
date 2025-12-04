import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { API } from '../api';
import type { Route } from '../api/routes';
import { PageLayout, PageLoader, EmptyState, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
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
    InstanceTabContent,
    VirtualizedRouteTable,
    useMockMode,
    useRouteData,
} from './route';

const RoutePage: React.FC = () => {
    const {
        instanceConfigs,
        instanceRoutes,
        selectedRoutes,
        loading,
        activeConfigTab,
        setInstanceConfigs,
        setInstanceRoutes,
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

    const { activeTab, setActiveTab, currentTabIndex } = useInstanceTabs({ items: instanceConfigs });

    const [deleteDialogOpen, setDeleteDialogOpen] = useState<boolean>(false);
    const [addDialogOpen, setAddDialogOpen] = useState<boolean>(false);
    const [addRouteForm, setAddRouteForm] = useState<AddRouteFormData>({
        configName: '',
        prefix: '',
        nexthopAddr: '',
        doFlush: false,
    });

    // Derived state
    const currentInstanceConfig = instanceConfigs[currentTabIndex];
    const currentInstance = currentInstanceConfig?.instance ?? currentTabIndex;
    const currentConfigs = currentInstanceConfig?.configs || [];
    const currentActiveConfig = activeConfigTab.get(currentInstance) || currentConfigs[0] || '';
    const currentSelectedMap = selectedRoutes.get(currentInstance);
    const currentSelected = currentSelectedMap?.get(currentActiveConfig);
    const isDeleteDisabled = !currentSelected || currentSelected.size === 0;

    const handleAddRouteClick = useCallback((): void => {
        setAddRouteForm({
            configName: currentActiveConfig,
            prefix: '',
            nexthopAddr: '',
            doFlush: false,
        });
        setAddDialogOpen(true);
    }, [currentActiveConfig]);

    const handleDeleteRouteClick = useCallback((): void => {
        if (isDeleteDisabled) {
            toaster.add({
                name: 'delete-route-warning',
                title: 'Warning',
                content: 'Please select routes to delete',
                theme: 'warning',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }
        setDeleteDialogOpen(true);
    }, [isDeleteDisabled]);

    const handleAddRouteConfirm = useCallback(async (): Promise<void> => {
        const configName = addRouteForm.configName.trim();

        if (!configName) {
            toaster.add({
                name: 'add-route-config-error',
                title: 'Error',
                content: 'Please enter a config name',
                theme: 'danger',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }

        if (!addRouteForm.prefix || !addRouteForm.nexthopAddr) {
            toaster.add({
                name: 'add-route-validation-error',
                title: 'Error',
                content: 'Please fill in all required fields',
                theme: 'danger',
                isClosable: true,
                autoHiding: 3000,
            });
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
            toaster.add({
                name: 'add-route-prefix-error',
                title: 'Error',
                content: errorMessage,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
            return;
        }

        const prefixLength = prefixResult.value.prefixLength;
        if (prefixLength === null) {
            toaster.add({
                name: 'add-route-prefix-error',
                title: 'Error',
                content: 'Invalid prefix length',
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
            return;
        }

        const nexthopResult = parseIPAddress(addRouteForm.nexthopAddr);
        if (!nexthopResult.ok) {
            let errorMessage = 'Invalid nexthop address format';
            if (nexthopResult.error === IPParseError.InvalidFormat) {
                errorMessage = 'Invalid nexthop address format. Use valid IPv4 (e.g., 192.168.1.1) or IPv6 (e.g., 2001:db8::1) address';
            }
            toaster.add({
                name: 'add-route-nexthop-error',
                title: 'Error',
                content: errorMessage,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
            return;
        }

        try {
            await API.route.insertRoute({
                target: {
                    configName,
                    dataplaneInstance: currentInstance,
                },
                prefix: addRouteForm.prefix,
                nexthopAddr: addRouteForm.nexthopAddr,
                doFlush: addRouteForm.doFlush,
            });

            setAddDialogOpen(false);

            const updatedConfigsList = currentConfigs.includes(configName)
                ? currentConfigs
                : [...currentConfigs, configName];

            if (!currentConfigs.includes(configName)) {
                setInstanceConfigs((prev) =>
                    prev.map((instConfig, instIdx) =>
                        instIdx === currentTabIndex
                            ? { ...instConfig, configs: updatedConfigsList }
                            : instConfig
                    )
                );

                setActiveConfigTab((prev) => {
                    const newMap = new Map(prev);
                    if (!newMap.has(currentInstance)) {
                        newMap.set(currentInstance, configName);
                    }
                    return newMap;
                });
            }

            const configRoutesMap = await reloadRoutes(currentInstance, updatedConfigsList);
            setInstanceRoutes((prev) => {
                const newMap = new Map(prev);
                newMap.set(currentInstance, configRoutesMap);
                return newMap;
            });

            toaster.add({
                name: 'add-route-success',
                title: 'Success',
                content: 'Route added successfully',
                theme: 'success',
                isClosable: true,
                autoHiding: 3000,
            });
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'add-route-error',
                title: 'Error',
                content: `Failed to add route: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
        }
    }, [addRouteForm, currentInstance, currentConfigs, currentTabIndex, reloadRoutes, setInstanceConfigs, setActiveConfigTab, setInstanceRoutes]);

    const handleDeleteConfirm = useCallback(async (): Promise<void> => {
        const selected = currentSelected;

        if (!selected || selected.size === 0) {
            setDeleteDialogOpen(false);
            return;
        }

        const configRoutesMap = instanceRoutes.get(currentInstance) || new Map<string, Route[]>();
        const routes = configRoutesMap.get(currentActiveConfig) || [];
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
                    target: {
                        configName: currentActiveConfig,
                        dataplaneInstance: currentInstance,
                    },
                    prefix: route.prefix,
                    nexthopAddr: route.nextHop,
                    doFlush: true,
                });
            }

            if (skippedInvalidRoute) {
                toaster.add({
                    name: 'delete-route-skip-warning',
                    title: 'Warning',
                    content: 'Skipped routes without prefix or nexthop address',
                    theme: 'warning',
                    isClosable: true,
                    autoHiding: 3000,
                });
            }

            const reloadedConfigRoutesMap = await reloadRoutes(currentInstance, currentConfigs);
            setInstanceRoutes((prev) => {
                const newMap = new Map(prev);
                newMap.set(currentInstance, reloadedConfigRoutesMap);
                return newMap;
            });

            setSelectedRoutes((prev) => {
                const newInstanceMap = new Map(prev.get(currentInstance) || new Map<string, Set<string>>());
                newInstanceMap.set(currentActiveConfig, new Set<string>());
                const newSelected = new Map(prev);
                newSelected.set(currentInstance, newInstanceMap);
                return newSelected;
            });

            setDeleteDialogOpen(false);

            toaster.add({
                name: 'delete-route-success',
                title: 'Success',
                content: `Deleted ${selectedRoutesList.length} route(s)`,
                theme: 'success',
                isClosable: true,
                autoHiding: 3000,
            });
        } catch (err) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error';
            toaster.add({
                name: 'delete-route-error',
                title: 'Error',
                content: `Failed to delete routes: ${errorMessage}`,
                theme: 'danger',
                isClosable: true,
                autoHiding: 5000,
            });
        }
    }, [currentSelected, currentInstance, currentActiveConfig, currentConfigs, instanceRoutes, reloadRoutes, setInstanceRoutes, setSelectedRoutes]);

    // Compute selected routes for delete dialog
    const selectedRoutesForDialog = useMemo((): Route[] => {
        if (!currentSelected || currentSelected.size === 0) return [];
        const configRoutesMap = instanceRoutes.get(currentInstance) || new Map<string, Route[]>();
        const routes = configRoutesMap.get(currentActiveConfig) || [];
        return routes.filter((route: Route) => currentSelected.has(getRouteId(route)));
    }, [currentSelected, currentInstance, currentActiveConfig, instanceRoutes]);

    const headerContent = (
        <RoutePageHeader
            onAddRoute={handleAddRouteClick}
            onDeleteRoute={handleDeleteRouteClick}
            isDeleteDisabled={mockEnabled ? mockSelectedIds.size === 0 : isDeleteDisabled}
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
                <Box style={{ width: '100%', flex: 1, minWidth: 0, minHeight: 0, padding: '20px', display: 'flex', flexDirection: 'column' }}>
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

    if (instanceConfigs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <EmptyState message="No instances found. Enable Mock Mode to test with sample data." />
                </Box>
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px', display: 'flex', flexDirection: 'column' }}>
                <InstanceTabs
                    items={instanceConfigs}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(instanceConfig, idx) => `Instance ${instanceConfig.instance ?? idx}`}
                    renderContent={(instanceConfig, idx) => {
                        const instance = instanceConfig.instance ?? idx;
                        const configs = instanceConfig.configs || [];
                        const configRoutesMap = instanceRoutes.get(instance) || new Map<string, Route[]>();
                        const activeConfig = activeConfigTab.get(instance) || configs[0] || '';
                        const instanceSelectedMap = selectedRoutes.get(instance) || new Map<string, Set<string>>();

                        const getRoutesData = (configName: string): ConfigRoutesData => {
                            const routes = configRoutesMap.get(configName) || [];
                            const selectedSet = instanceSelectedMap.get(configName) || new Set<string>();
                            return {
                                routes,
                                selectedIds: Array.from(selectedSet),
                            };
                        };

                        return (
                            <InstanceTabContent
                                configs={configs}
                                activeConfig={activeConfig}
                                onConfigChange={(config) => handleConfigTabChange(instance, config)}
                                getRoutesData={getRoutesData}
                                onSelectionChange={(configName, ids) => handleSelectionChange(instance, configName, ids)}
                                getRouteId={getRouteId}
                            />
                        );
                    }}
                    contentStyle={{ flex: 1, minHeight: 0 }}
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
