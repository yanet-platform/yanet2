import React, { useState, useCallback, useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
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
import './decap/decap.css';

const DecapPage: React.FC = () => {
    const {
        data,
        loading,
        selectedPrefixes,
        handleSelectionChange,
        addConfig,
        addPrefixes,
        removePrefixes,
    } = useDecapData();

    const [addPrefixDialogOpen, setAddPrefixDialogOpen] = useState(false);
    const [deletePrefixDialogOpen, setDeletePrefixDialogOpen] = useState(false);
    const [addConfigDialogOpen, setAddConfigDialogOpen] = useState(false);
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');

    const configs = data.configs;
    const currentActiveConfig = activeConfigTab || configs[0] || '';
    const currentSelected = selectedPrefixes.get(currentActiveConfig) || new Set<string>();
    const isDeleteDisabled = currentSelected.size === 0 || !currentActiveConfig;

    const handleConfigTabChange = useCallback((config: string) => {
        setActiveConfigTab(config);
    }, []);

    const handleAddConfig = useCallback(() => {
        setAddConfigDialogOpen(true);
    }, []);

    const handleAddConfigConfirm = useCallback((configName: string) => {
        addConfig(configName);
        // Switch to the new config tab
        setActiveConfigTab(configName);
    }, [addConfig]);

    const handleAddPrefix = useCallback(() => {
        setAddPrefixDialogOpen(true);
    }, []);

    const handleDeletePrefixes = useCallback(() => {
        setDeletePrefixDialogOpen(true);
    }, []);

    const handleAddPrefixConfirm = useCallback(async (prefixes: string[]) => {
        await addPrefixes(currentActiveConfig, prefixes);
    }, [addPrefixes, currentActiveConfig]);

    const handleDeletePrefixConfirm = useCallback(async () => {
        const prefixesToDelete = Array.from(currentSelected);
        await removePrefixes(currentActiveConfig, prefixesToDelete);
    }, [removePrefixes, currentActiveConfig, currentSelected]);

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

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box className="decap-page__empty">
                    <EmptyState message="No decap configurations found. Click 'Add Config' to create one." />
                </Box>

                <AddConfigDialog
                    open={addConfigDialogOpen}
                    onClose={() => setAddConfigDialogOpen(false)}
                    onConfirm={handleAddConfigConfirm}
                    existingConfigs={configs}
                />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box className="decap-page__content">
                <ConfigTabs
                    configs={configs}
                    activeConfig={currentActiveConfig}
                    onConfigChange={handleConfigTabChange}
                    renderContent={(configName) => {
                        const prefixes = data.configPrefixes.get(configName) || [];
                        const prefixItems = prefixesToItems(prefixes);
                        const configSelected = selectedPrefixes.get(configName) || new Set<string>();

                        return (
                            <PrefixTable
                                prefixes={prefixItems}
                                selectedIds={configSelected}
                                onSelectionChange={(ids) => handleSelectionChange(configName, ids)}
                                onAddPrefix={handleAddPrefix}
                            />
                        );
                    }}
                />
            </Box>

            <AddConfigDialog
                open={addConfigDialogOpen}
                onClose={() => setAddConfigDialogOpen(false)}
                onConfirm={handleAddConfigConfirm}
                existingConfigs={configs}
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
