import React, { useState, useCallback, useMemo } from 'react';
import { Box } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, EmptyState } from '../components';
import type { Rule } from '../api/forward';
import type { RuleItem } from './forward/types';
import {
    ForwardPageHeader,
    RuleTable,
    AddRuleDialog,
    EditRuleDialog,
    DeleteRuleDialog,
    ConfigTabs,
    useForwardData,
    rulesToItems,
} from './forward';
import './forward/forward.css';

const ForwardPage: React.FC = () => {
    const {
        data,
        loading,
        selectedRules,
        handleSelectionChange,
        addConfig,
        addRule,
        updateRule,
        removeRules,
    } = useForwardData();

    const [addRuleDialogOpen, setAddRuleDialogOpen] = useState(false);
    const [editRuleDialogOpen, setEditRuleDialogOpen] = useState(false);
    const [deleteRuleDialogOpen, setDeleteRuleDialogOpen] = useState(false);
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');
    const [editingRule, setEditingRule] = useState<RuleItem | null>(null);

    const configs = data.configs;
    const currentActiveConfig = activeConfigTab || configs[0] || '';
    const currentSelected = selectedRules.get(currentActiveConfig) || new Set<string>();
    const isDeleteDisabled = currentSelected.size === 0 || !currentActiveConfig;

    const handleConfigTabChange = useCallback((config: string) => {
        setActiveConfigTab(config);
    }, []);

    const handleAddRule = useCallback(() => {
        setAddRuleDialogOpen(true);
    }, []);

    const handleDeleteRules = useCallback(() => {
        setDeleteRuleDialogOpen(true);
    }, []);

    const handleEditRule = useCallback((ruleItem: RuleItem) => {
        setEditingRule(ruleItem);
        setEditRuleDialogOpen(true);
    }, []);

    const handleAddRuleConfirm = useCallback(async (configName: string, rule: Rule) => {
        // Create config if it doesn't exist
        if (!configs.includes(configName)) {
            addConfig(configName);
        }
        await addRule(configName, rule);
        // Switch to the config tab where rule was added
        setActiveConfigTab(configName);
    }, [configs, addConfig, addRule]);

    const handleEditRuleConfirm = useCallback(async (rule: Rule) => {
        if (!editingRule) return;
        await updateRule(currentActiveConfig, editingRule.index, rule);
    }, [editingRule, currentActiveConfig, updateRule]);

    const handleDeleteRuleConfirm = useCallback(async () => {
        // Extract indices from selected rule ids (format: "rule-{index}")
        const indicesToRemove = Array.from(currentSelected)
            .map((id) => parseInt(id.replace('rule-', ''), 10))
            .filter((idx) => !isNaN(idx));
        await removeRules(currentActiveConfig, indicesToRemove);
    }, [removeRules, currentActiveConfig, currentSelected]);

    const headerContent = (
        <ForwardPageHeader
            onAddRule={handleAddRule}
            onDeleteRules={handleDeleteRules}
            isDeleteDisabled={isDeleteDisabled}
        />
    );

    if (loading) {
        return (
            <PageLayout title="Forward">
                <PageLoader loading={loading} size="l" />
            </PageLayout>
        );
    }

    if (configs.length === 0) {
        return (
            <PageLayout header={headerContent}>
                <Box className="forward-page__empty">
                    <EmptyState message="No forward configurations found. Click 'Add Rule' to create one." />
                </Box>

                <AddRuleDialog
                    open={addRuleDialogOpen}
                    onClose={() => setAddRuleDialogOpen(false)}
                    onConfirm={handleAddRuleConfirm}
                    existingConfigs={configs}
                />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={headerContent}>
            <Box className="forward-page__content">
                <ConfigTabs
                    configs={configs}
                    activeConfig={currentActiveConfig}
                    onConfigChange={handleConfigTabChange}
                    renderContent={(configName) => {
                        const rules = data.configRules.get(configName) || [];
                        const ruleItems = rulesToItems(rules);
                        const configSelected = selectedRules.get(configName) || new Set<string>();

                        return (
                            <RuleTable
                                rules={ruleItems}
                                selectedIds={configSelected}
                                onSelectionChange={(ids) => handleSelectionChange(configName, ids)}
                                onEditRule={handleEditRule}
                            />
                        );
                    }}
                />
            </Box>

            <AddRuleDialog
                open={addRuleDialogOpen}
                onClose={() => setAddRuleDialogOpen(false)}
                onConfirm={handleAddRuleConfirm}
                existingConfigs={configs}
                currentConfig={currentActiveConfig}
            />

            <EditRuleDialog
                open={editRuleDialogOpen}
                onClose={() => {
                    setEditRuleDialogOpen(false);
                    setEditingRule(null);
                }}
                onConfirm={handleEditRuleConfirm}
                rule={editingRule?.rule || null}
                ruleIndex={editingRule?.index ?? -1}
            />

            <DeleteRuleDialog
                open={deleteRuleDialogOpen}
                onClose={() => setDeleteRuleDialogOpen(false)}
                onConfirm={handleDeleteRuleConfirm}
                selectedCount={currentSelected.size}
            />
        </PageLayout>
    );
};

export default ForwardPage;
