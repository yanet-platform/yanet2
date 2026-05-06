import React, { useState, useCallback, useMemo } from 'react';
import { Box, Flex, Text } from '@gravity-ui/uikit';
import {
    PageLayout,
    PageLoader,
    EmptyState,
    ConfirmDialog,
    ConfigTabs,
} from '../../../components';
import {
    DecapPageHeader,
    PrefixTable,
    AddPrefixDialog,
    useDecapData,
    prefixesToItems,
} from '.';
import './decap.scss';

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
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');

    const configs = data.configs;
    const currentActiveConfig = activeConfigTab || configs[0] || '';
    const currentSelected = selectedPrefixes.get(currentActiveConfig) || new Set<string>();
    const isDeleteDisabled = currentSelected.size === 0 || !currentActiveConfig;

    const handleConfigTabChange = useCallback((config: string) => {
        setActiveConfigTab(config);
    }, []);

    const handleAddPrefix = useCallback(() => {
        setAddPrefixDialogOpen(true);
    }, []);

    const handleDeletePrefixes = useCallback(() => {
        setDeletePrefixDialogOpen(true);
    }, []);

    const handleAddPrefixConfirm = useCallback(async (configName: string, prefixes: string[]) => {
        // Create config if it doesn't exist
        if (!configs.includes(configName)) {
            addConfig(configName);
        }
        await addPrefixes(configName, prefixes);
        // Switch to the config tab where prefix was added
        setActiveConfigTab(configName);
    }, [configs, addConfig, addPrefixes]);

    const handleDeletePrefixConfirm = useCallback(async () => {
        const prefixesToDelete = Array.from(currentSelected);
        await removePrefixes(currentActiveConfig, prefixesToDelete);
    }, [removePrefixes, currentActiveConfig, currentSelected]);

    const selectedPrefixesList = useMemo(() => {
        return Array.from(currentSelected);
    }, [currentSelected]);

    const headerContent = (
        <DecapPageHeader
            onAddPrefix={handleAddPrefix}
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
                <EmptyState message="No decap configurations found. Click 'Add Prefix' to create one." />

                <AddPrefixDialog
                    open={addPrefixDialogOpen}
                    onClose={() => setAddPrefixDialogOpen(false)}
                    onConfirm={handleAddPrefixConfirm}
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
                            />
                        );
                    }}
                />
            </Box>

            <AddPrefixDialog
                open={addPrefixDialogOpen}
                onClose={() => setAddPrefixDialogOpen(false)}
                onConfirm={handleAddPrefixConfirm}
                existingConfigs={configs}
            />

            <ConfirmDialog
                open={deletePrefixDialogOpen}
                onClose={() => setDeletePrefixDialogOpen(false)}
                onConfirm={async () => {
                    await handleDeletePrefixConfirm();
                    setDeletePrefixDialogOpen(false);
                }}
                title="Delete Prefixes"
                message={`Are you sure you want to delete ${selectedPrefixesList.length} prefix(es)?`}
                confirmText="Delete"
                danger
                disabled={selectedPrefixesList.length === 0}
            >
                <Box
                    style={{
                        backgroundColor: 'var(--g-color-base-generic)',
                        borderRadius: 8,
                        padding: 12,
                        maxHeight: 200,
                        overflow: 'auto',
                    }}
                >
                    <Flex direction="column" gap={1}>
                        {selectedPrefixesList.slice(0, 10).map((prefix, idx) => (
                            <Text key={idx} variant="code-1">{prefix}</Text>
                        ))}
                        {selectedPrefixesList.length > 10 && (
                            <Text variant="body-1" color="secondary">
                                ... and {selectedPrefixesList.length - 10} more
                            </Text>
                        )}
                    </Flex>
                </Box>
            </ConfirmDialog>
        </PageLayout>
    );
};

export default DecapPage;
