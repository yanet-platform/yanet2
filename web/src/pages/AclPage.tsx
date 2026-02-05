import React, { useState, useCallback, useMemo, useEffect } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import type { Rule } from '../api/acl';
import { useSidebarContext } from '../types';
import {
    useAclData,
    AclPageHeader,
    ConfigTabs,
    InnerTabs,
    VirtualizedAclTable,
    FWStateForm,
    UploadYamlDialog,
    CreateConfigDialog,
    DeleteConfigDialog,
    UnsavedChangesDialog,
} from './acl';
import './acl/acl.css';

const AclPage: React.FC = () => {
    const { setSidebarDisabled } = useSidebarContext();
    const {
        configs,
        configData,
        loading,
        saving,
        activeConfigTab,
        activeInnerTab,
        handleConfigTabChange,
        handleInnerTabChange,
        hasUnsavedChanges,
        hasAnyUnsavedChanges,
        addNewConfig,
        updateConfigRules,
        updateConfigFWState,
        saveConfig,
        saveFWStateConfig,
        deleteConfig,
        getConfigState,
    } = useAclData();

    // Dialog states
    const [uploadDialogOpen, setUploadDialogOpen] = useState(false);
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [unsavedDialogOpen, setUnsavedDialogOpen] = useState(false);
    const [pendingConfigChange, setPendingConfigChange] = useState<string | null>(null);
    
    // Check if there are any unsaved changes
    const anyUnsavedChanges = hasAnyUnsavedChanges();
    
    // Browser beforeunload warning - warns when closing tab/browser
    useEffect(() => {
        const handleBeforeUnload = (e: BeforeUnloadEvent) => {
            if (anyUnsavedChanges) {
                e.preventDefault();
                e.returnValue = '';
            }
        };
        
        window.addEventListener('beforeunload', handleBeforeUnload);
        return () => window.removeEventListener('beforeunload', handleBeforeUnload);
    }, [anyUnsavedChanges]);

    // Disable sidebar during save operation
    useEffect(() => {
        setSidebarDisabled(saving);
    }, [saving, setSidebarDisabled]);

    // Search state for rules table
    const [searchQuery, setSearchQuery] = useState('');

    // Current config data
    const currentConfigData = configData.get(activeConfigTab);
    const currentRules = currentConfigData?.rules || [];
    const currentFwstateMap = currentConfigData?.fwstateMap;
    const currentFwstateSync = currentConfigData?.fwstateSync;
    const currentHasUnsavedChanges = activeConfigTab ? hasUnsavedChanges(activeConfigTab) : false;

    // Reset search when changing config
    useEffect(() => {
        setSearchQuery('');
    }, [activeConfigTab]);

    // Derived state
    const isSaveDisabled = !activeConfigTab || !currentHasUnsavedChanges;
    const isDeleteDisabled = !activeConfigTab;

    // Config state map for tabs
    const configStates = useMemo(() => {
        const states = new Map<string, 'saved' | 'modified' | 'new'>();
        for (const config of configs) {
            states.set(config, getConfigState(config));
        }
        return states;
    }, [configs, getConfigState]);

    // Handlers
    const handleUploadYaml = useCallback(() => {
        setUploadDialogOpen(true);
    }, []);

    const handleUploadConfirm = useCallback((configName: string, rules: Rule[]) => {
        if (configs.includes(configName)) {
            // Update existing config
            updateConfigRules(configName, rules);
            handleConfigTabChange(configName);
        } else {
            // Create new config
            addNewConfig(configName, rules);
        }
        setUploadDialogOpen(false);
    }, [configs, updateConfigRules, handleConfigTabChange, addNewConfig]);

    const handleCreateConfirm = useCallback((configName: string) => {
        addNewConfig(configName, []);
        setCreateDialogOpen(false);
    }, [addNewConfig]);

    const handleSave = useCallback(async () => {
        if (!activeConfigTab) return;
        
        if (activeInnerTab === 'rules') {
            await saveConfig(activeConfigTab);
        } else {
            await saveFWStateConfig(activeConfigTab);
        }
    }, [activeConfigTab, activeInnerTab, saveConfig, saveFWStateConfig]);

    const handleDeleteConfig = useCallback(() => {
        setDeleteDialogOpen(true);
    }, []);

    const handleDeleteConfirm = useCallback(async () => {
        if (!activeConfigTab) return;
        await deleteConfig(activeConfigTab);
        setDeleteDialogOpen(false);
    }, [activeConfigTab, deleteConfig]);

    const handleTryConfigChange = useCallback((config: string) => {
        if (activeConfigTab && hasUnsavedChanges(activeConfigTab)) {
            setPendingConfigChange(config);
            setUnsavedDialogOpen(true);
        } else {
            handleConfigTabChange(config);
        }
    }, [activeConfigTab, hasUnsavedChanges, handleConfigTabChange]);

    const handleUnsavedDiscard = useCallback(() => {
        if (pendingConfigChange) {
            handleConfigTabChange(pendingConfigChange);
        }
        setUnsavedDialogOpen(false);
        setPendingConfigChange(null);
    }, [pendingConfigChange, handleConfigTabChange]);

    const handleUnsavedSave = useCallback(async () => {
        if (activeConfigTab) {
            await saveConfig(activeConfigTab);
        }
        if (pendingConfigChange) {
            handleConfigTabChange(pendingConfigChange);
        }
        setUnsavedDialogOpen(false);
        setPendingConfigChange(null);
    }, [activeConfigTab, pendingConfigChange, saveConfig, handleConfigTabChange]);
    
    const handleUnsavedClose = useCallback(() => {
        setUnsavedDialogOpen(false);
        setPendingConfigChange(null);
    }, []);

    const handleFWStateMapChange = useCallback((mapConfig: typeof currentFwstateMap) => {
        if (activeConfigTab) {
            updateConfigFWState(activeConfigTab, mapConfig, currentFwstateSync);
        }
    }, [activeConfigTab, currentFwstateSync, updateConfigFWState]);

    const handleFWStateSyncChange = useCallback((syncConfig: typeof currentFwstateSync) => {
        if (activeConfigTab) {
            updateConfigFWState(activeConfigTab, currentFwstateMap, syncConfig);
        }
    }, [activeConfigTab, currentFwstateMap, updateConfigFWState]);

    const handleFWStateSave = useCallback(async () => {
        if (activeConfigTab) {
            await saveFWStateConfig(activeConfigTab);
        }
    }, [activeConfigTab, saveFWStateConfig]);

    const headerContent = (
        <AclPageHeader
            onUploadYaml={handleUploadYaml}
            onSave={handleSave}
            onDeleteConfig={handleDeleteConfig}
            isSaveDisabled={isSaveDisabled}
            isDeleteDisabled={isDeleteDisabled}
            hasUnsavedChanges={currentHasUnsavedChanges}
            isSaving={saving}
        />
    );

    if (loading) {
        return (
            <PageLayout title="ACL">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box className="acl-page__empty-content">
                    <EmptyState message="No ACL configurations found. Click 'Upload YAML' to create one." />
                </Box>

                <UploadYamlDialog
                    open={uploadDialogOpen}
                    onClose={() => setUploadDialogOpen(false)}
                    onConfirm={handleUploadConfirm}
                    existingConfigs={configs}
                />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box className="acl-page__content">
                <ConfigTabs
                    configs={configs}
                    activeConfig={activeConfigTab}
                    configStates={configStates}
                    onConfigChange={handleTryConfigChange}
                    onTryChangeConfig={handleTryConfigChange}
                />

                <InnerTabs
                    activeTab={activeInnerTab}
                    onTabChange={handleInnerTabChange}
                />

                <Box className="acl-page__table-container">
                    {activeInnerTab === 'rules' ? (
                        <VirtualizedAclTable
                            key={activeConfigTab}
                            rules={currentRules}
                            searchQuery={searchQuery}
                            onSearchChange={setSearchQuery}
                            isLoading={saving}
                        />
                    ) : (
                        <FWStateForm
                            mapConfig={currentFwstateMap}
                            syncConfig={currentFwstateSync}
                            onMapConfigChange={handleFWStateMapChange}
                            onSyncConfigChange={handleFWStateSyncChange}
                            onSave={handleFWStateSave}
                            hasChanges={currentHasUnsavedChanges}
                        />
                    )}
                </Box>
            </Box>

            <UploadYamlDialog
                open={uploadDialogOpen}
                onClose={() => setUploadDialogOpen(false)}
                onConfirm={handleUploadConfirm}
                existingConfigs={configs}
            />

            <CreateConfigDialog
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={handleCreateConfirm}
                existingConfigs={configs}
            />

            <DeleteConfigDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDeleteConfirm}
                configName={activeConfigTab}
            />

            <UnsavedChangesDialog
                open={unsavedDialogOpen}
                onClose={handleUnsavedClose}
                onDiscard={handleUnsavedDiscard}
                onSave={handleUnsavedSave}
                configName={activeConfigTab}
            />
        </PageLayout>
    );
};

export default AclPage;
