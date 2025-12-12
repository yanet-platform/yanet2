import React, { useState, useCallback, useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState, InstanceTabs } from '../components';
import { useInstanceTabs } from '../hooks';
import {
    DecapPageHeader,
    PrefixTable,
    AddPrefixDialog,
    DeletePrefixDialog,
    AddConfigDialog,
    useDecapData,
    prefixesToItems,
    ConfigTabs,
} from './decap';

const DecapPage: React.FC = () => {
    const {
        instances,
        loading,
        selectedPrefixes,
        handleSelectionChange,
        addConfig,
        addPrefixes,
        removePrefixes,
    } = useDecapData();

    const { activeTab, setActiveTab, currentTabIndex } = useInstanceTabs({ items: instances });

    const [addPrefixDialogOpen, setAddPrefixDialogOpen] = useState(false);
    const [deletePrefixDialogOpen, setDeletePrefixDialogOpen] = useState(false);
    const [addConfigDialogOpen, setAddConfigDialogOpen] = useState(false);
    const [activeConfigTab, setActiveConfigTab] = useState<Map<number, string>>(new Map());

    const currentInstance = instances[currentTabIndex];
    const currentInstanceNumber = currentInstance?.instance ?? currentTabIndex;
    const currentConfigs = currentInstance?.configs || [];
    const currentActiveConfig = activeConfigTab.get(currentInstanceNumber) || currentConfigs[0] || '';
    
    const currentSelectedMap = selectedPrefixes.get(currentInstanceNumber);
    const currentSelected = currentSelectedMap?.get(currentActiveConfig) || new Set<string>();
    const isDeleteDisabled = currentSelected.size === 0 || !currentActiveConfig;

    const handleConfigTabChange = useCallback((instance: number, config: string) => {
        setActiveConfigTab((prev) => {
            const newMap = new Map(prev);
            newMap.set(instance, config);
            return newMap;
        });
    }, []);

    const handleAddConfig = useCallback(() => {
        setAddConfigDialogOpen(true);
    }, []);

    const handleAddConfigConfirm = useCallback((configName: string) => {
        addConfig(currentInstanceNumber, configName);
        // Switch to the new config tab
        setActiveConfigTab((prev) => {
            const newMap = new Map(prev);
            newMap.set(currentInstanceNumber, configName);
            return newMap;
        });
    }, [addConfig, currentInstanceNumber]);

    const handleAddPrefix = useCallback(() => {
        setAddPrefixDialogOpen(true);
    }, []);

    const handleDeletePrefixes = useCallback(() => {
        setDeletePrefixDialogOpen(true);
    }, []);

    const handleAddPrefixConfirm = useCallback(async (prefixes: string[]) => {
        await addPrefixes(currentInstanceNumber, currentActiveConfig, prefixes);
    }, [addPrefixes, currentInstanceNumber, currentActiveConfig]);

    const handleDeletePrefixConfirm = useCallback(async () => {
        const prefixesToDelete = Array.from(currentSelected);
        await removePrefixes(currentInstanceNumber, currentActiveConfig, prefixesToDelete);
    }, [removePrefixes, currentInstanceNumber, currentActiveConfig, currentSelected]);

    const selectedPrefixesList = useMemo(() => {
        return Array.from(currentSelected);
    }, [currentSelected]);

    const headerContent = (
        <DecapPageHeader
            onAddConfig={handleAddConfig}
            onDeletePrefixes={handleDeletePrefixes}
            isDeleteDisabled={isDeleteDisabled}
        />
    );

    if (loading) {
        return (
            <PageLayout title="Decap">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (instances.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box style={{ width: '100%', flex: 1, minWidth: 0, padding: '20px' }}>
                    <EmptyState message="No instances found." />
                </Box>
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box style={{ width: '100%', height: '100%', flex: 1, minWidth: 0, padding: '20px', display: 'flex', flexDirection: 'column' }}>
                <InstanceTabs
                    items={instances}
                    activeTab={activeTab}
                    onTabChange={setActiveTab}
                    getTabLabel={(inst, idx) => `Instance ${inst.instance ?? idx}`}
                    renderContent={(inst) => {
                        const instanceNum = inst.instance;
                        const configs = inst.configs;
                        const activeConfig = activeConfigTab.get(instanceNum) || configs[0] || '';
                        const instanceSelectedMap = selectedPrefixes.get(instanceNum) || new Map<string, Set<string>>();

                        if (configs.length === 0) {
                            return (
                                <EmptyState message="No decap configurations found. Click 'Add Config' to create one." />
                            );
                        }

                        return (
                            <ConfigTabs
                                configs={configs}
                                activeConfig={activeConfig}
                                onConfigChange={(config) => handleConfigTabChange(instanceNum, config)}
                                renderContent={(configName) => {
                                    const prefixes = inst.configPrefixes.get(configName) || [];
                                    const prefixItems = prefixesToItems(prefixes);
                                    const configSelected = instanceSelectedMap.get(configName) || new Set<string>();

                                    return (
                                        <PrefixTable
                                            prefixes={prefixItems}
                                            selectedIds={configSelected}
                                            onSelectionChange={(ids) => handleSelectionChange(instanceNum, configName, ids)}
                                            onAddPrefix={handleAddPrefix}
                                        />
                                    );
                                }}
                            />
                        );
                    }}
                    contentStyle={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}
                />
            </Box>

            <AddConfigDialog
                open={addConfigDialogOpen}
                onClose={() => setAddConfigDialogOpen(false)}
                onConfirm={handleAddConfigConfirm}
                existingConfigs={currentConfigs}
            />

            <AddPrefixDialog
                open={addPrefixDialogOpen}
                onClose={() => setAddPrefixDialogOpen(false)}
                onConfirm={handleAddPrefixConfirm}
            />

            <DeletePrefixDialog
                open={deletePrefixDialogOpen}
                onClose={() => setDeletePrefixDialogOpen(false)}
                onConfirm={handleDeletePrefixConfirm}
                selectedPrefixes={selectedPrefixesList}
            />
        </PageLayout>
    );
};

export default DecapPage;
