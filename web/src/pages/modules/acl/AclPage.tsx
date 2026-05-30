import React, { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Label, Text } from '@gravity-ui/uikit';
import { Pause, Play, Plus } from '@gravity-ui/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput } from '../../../components';
import { useAclDraft } from './useAclDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { Rule } from '../../../api/acl';
import { ActionKind } from '../../../api/acl';
import type { RuleItem, RuleDraft } from './types';
import { rulesToNgItems, draftToRule, useKeyboardShortcuts } from './hooks';
import { DRAWER_TRANSITION_MS } from './RuleTable';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO, { type ImportMode } from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useAclRuleCounters } from './useAclRuleCounters';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal } from '../../_shared/draft';
import '../../../styles/draft-page.scss';
import './acl.scss';

const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

const AclPage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        draftRules,
        draftRuleIds,
        serverRules,
        fwstateName,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    } = useAclDraft();
    const [searchParams, setSearchParams] = useSearchParams();

    const [paused, setPaused] = useState(false);
    const [enabledCounterNames, setEnabledCounterNames] = useState<Set<string>>(new Set());
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [drawer, setDrawer] = useState<{ open: boolean; mode: 'add' | 'edit'; item: RuleItem | null }>({
        open: false,
        mode: 'add',
        item: null,
    });
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [deleteConfigTarget, setDeleteConfigTarget] = useState<string | null>(null);
    const [deleteInFlightConfig, setDeleteInFlightConfig] = useState<string | null>(null);
    const [bulkDeleteConfig, setBulkDeleteConfig] = useState<string | null>(null);
    const [bulkDeleteRuleIds, setBulkDeleteRuleIds] = useState<string[]>([]);
    const drawerRef = useRef<RuleDrawerHandle>(null);
    const navigate = useNavigate();
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const search = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig) || queryConfig === deleteInFlightConfig))
        ? queryConfig
        : (draftConfigs[0] || '');
    const updateParams = useCallback((updates: Record<string, string | null>): void => {
        setSearchParams((prev) => {
            const next = new URLSearchParams(prev);
            for (const [key, value] of Object.entries(updates)) {
                if (value === null || value === '') {
                    next.delete(key);
                } else {
                    next.set(key, value);
                }
            }
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    const clearConfigParamIfCurrent = useCallback((name: string): void => {
        setSearchParams((prev) => {
            if (prev.get(QP_CONFIG) !== name) {
                return prev;
            }
            const next = new URLSearchParams(prev);
            next.delete(QP_CONFIG);
            return next;
        }, { replace: true });
    }, [setSearchParams]);

    useEffect(() => {
        const updates: Record<string, string | null> = {};
        if (!loading) {
            if (!currentConfig) {
                if (searchParams.get(QP_CONFIG) !== null) {
                    updates[QP_CONFIG] = null;
                }
            } else if (queryConfig !== currentConfig) {
                updates[QP_CONFIG] = currentConfig;
            }
        }
        if (Object.keys(updates).length > 0) {
            updateParams(updates);
        }
    }, [currentConfig, loading, queryConfig, searchParams, updateParams]);

    useUnsavedChangesBlocker(anyDirty);

    useEffect(() => {
        setSelectedIds(new Set());
        setActiveRowId(null);
        setDrawer((d) => ({ ...d, open: false, item: null }));
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
        setDeleteConfigTarget(null);
        setBulkDeleteConfig(null);
        setBulkDeleteRuleIds([]);
        setEnabledCounterNames(new Set());
        setPaused(false);
    }, [currentConfig]);

    const currentFwStateName = fwstateName(currentConfig);
    const rawRules: Rule[] = draftRules(currentConfig);
    const rawIds: string[] = draftRuleIds(currentConfig);
    const allItems = useMemo(() => rulesToNgItems(rawRules, rawIds), [rawRules, rawIds]);

    const { rates } = useAclRuleCounters(currentConfig, allItems, enabledCounterNames, !paused);

    const ruleCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach(c => m.set(c, draftRules(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRules]);

    const dirtySet = useMemo((): Set<string> => {
        const s = new Set<string>();
        draftConfigs.forEach(c => { if (isDirty(c)) s.add(c); });
        return s;
    }, [draftConfigs, isDirty]);

    const deferredSearch = useDeferredValue(search);

    const visibleItems = useMemo((): RuleItem[] => {
        const q = deferredSearch.trim().toLowerCase();
        if (!q) return allItems;
        return allItems.filter(item => item.searchText.includes(q));
    }, [allItems, deferredSearch]);

    const openAdd = useCallback((): void => {
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: null });
    }, []);

    const openEdit = useCallback((item: RuleItem): void => {
        setActiveRowId(item.id);
        setDrawer({ open: true, mode: 'edit', item });
    }, []);

    const closeDrawer = useCallback((): void => {
        setDrawer(d => ({ ...d, open: false }));
        setTimeout(() => {
            setActiveRowId(null);
            setDrawer(d => ({ ...d, item: null }));
        }, DRAWER_TRANSITION_MS);
    }, []);

    const handleDrawerApply = useCallback((draft: RuleDraft): void => {
        const rule = draftToRule(draft);
        if (drawer.mode === 'add') {
            dispatchDraft({ type: 'ADD_RULE', configName: currentConfig, rule });
        } else if (drawer.item) {
            dispatchDraft({ type: 'UPDATE_RULE_AT_INDEX', configName: currentConfig, index: drawer.item.index, rule });
        }
        closeDrawer();
    }, [drawer, currentConfig, dispatchDraft, closeDrawer]);

    const handleDeleteItem = useCallback((item: RuleItem): void => {
        dispatchDraft({ type: 'REMOVE_RULES', configName: currentConfig, indices: [item.index] });
        closeDrawer();
    }, [currentConfig, dispatchDraft, closeDrawer]);

    const handleDuplicate = useCallback((item: RuleItem): void => {
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: { ...item, rule: { ...item.rule } } });
    }, []);

    const handleOpenBulkDelete = useCallback((): void => {
        if (!currentConfig) {
            return;
        }
        setBulkDeleteConfig(currentConfig);
        setBulkDeleteRuleIds(Array.from(selectedIds));
        setDeleteConfirmOpen(true);
    }, [currentConfig, selectedIds]);

    const handleCloseBulkDelete = useCallback((): void => {
        setDeleteConfirmOpen(false);
        setBulkDeleteConfig(null);
        setBulkDeleteRuleIds([]);
    }, []);

    const handleBulkDelete = useCallback((): void => {
        if (!bulkDeleteConfig) {
            handleCloseBulkDelete();
            return;
        }
        const selectedIdSet = new Set(bulkDeleteRuleIds);
        const targetIds = draftRuleIds(bulkDeleteConfig);
        const indices = targetIds
            .flatMap((id, index) => (selectedIdSet.has(id) ? [index] : []));

        dispatchDraft({ type: 'REMOVE_RULES', configName: bulkDeleteConfig, indices });
        setSelectedIds(new Set());
        setBulkDeleteConfig(null);
        setBulkDeleteRuleIds([]);
        setDeleteConfirmOpen(false);
    }, [bulkDeleteConfig, bulkDeleteRuleIds, draftRuleIds, dispatchDraft, handleCloseBulkDelete]);

    const handleOpenDeleteConfig = useCallback((): void => {
        if (!currentConfig) {
            return;
        }
        setDeleteConfigTarget(currentConfig);
        setDeleteConfigOpen(true);
    }, [currentConfig]);

    const handleCloseDeleteConfig = useCallback((): void => {
        setDeleteConfigOpen(false);
        setDeleteConfigTarget(null);
    }, []);

    const handleDeleteConfig = useCallback(async (): Promise<void> => {
        if (!deleteConfigTarget) {
            setDeleteConfigOpen(false);
            return;
        }
        const name = deleteConfigTarget;
        setDeleteConfigOpen(false);
        setDeleteInFlightConfig(name);
        try {
            await commitDeleteConfig(name);
            clearConfigParamIfCurrent(name);
        } catch {
            // Toast already surfaced by the hook.
        } finally {
            setDeleteInFlightConfig(null);
            setDeleteConfigTarget(null);
        }
    }, [deleteConfigTarget, commitDeleteConfig, clearConfigParamIfCurrent]);

    const handleSave = useCallback(async (): Promise<void> => {
        await saveConfig(currentConfig);
        setDiffModalOpen(false);
    }, [currentConfig, saveConfig]);

    const handleSavePress = useCallback((): void => {
        if (drawer.open) {
            drawerRef.current?.flushAndApply();
        }
        setDiffModalOpen(true);
    }, [drawer.open]);

    const handleToggleCounter = useCallback((counterName: string): void => {
        setEnabledCounterNames(prev => {
            const next = new Set(prev);
            if (next.has(counterName)) {
                next.delete(counterName);
            } else {
                next.add(counterName);
            }
            return next;
        });
    }, []);

    const handleDiscard = useCallback((): void => {
        discardConfig(currentConfig);
    }, [currentConfig, discardConfig]);

    const handleImportYaml = useCallback((importedConfigName: string, rules: Rule[], mode: ImportMode): void => {
        const target = importedConfigName || currentConfig;
        if (mode === 'append') {
            const current = draftRules(target);
            dispatchDraft({ type: 'REPLACE_ALL_RULES', configName: target, rules: [...current, ...rules] });
        } else {
            dispatchDraft({ type: 'REPLACE_ALL_RULES', configName: target, rules });
        }
        updateParams({ [QP_CONFIG]: target || null });
    }, [currentConfig, draftRules, dispatchDraft, updateParams]);

    const handleTabSelect = useCallback((cfg: string): void => {
        updateParams({ [QP_CONFIG]: cfg || null });
    }, [updateParams]);

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    useKeyboardShortcuts({
        onNewRule: openAdd,
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);
    const hasStatefulRules = useMemo(() =>
        rawRules.some((rule) => (rule.actions ?? []).some((action) =>
            action.kind === ActionKind.ACTION_KIND_CHECK_STATE || action.kind === ActionKind.ACTION_KIND_CREATE_STATE,
        )), [rawRules]);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">ACL</Text>
            {currentFwStateName && (
                <Button size="s" view="outlined" onClick={() => navigate('/modules/fwstate')}>
                    FWState: {currentFwStateName}
                </Button>
            )}
            {!currentFwStateName && hasStatefulRules && (
                <Label theme="warning">Stateful rules without FWState</Label>
            )}
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <SearchInput
                    value={search}
                    onUpdate={handleSearchChange}
                    placeholder="Search rules…"
                />
            </div>
            {enabledCounterNames.size > 0 && (
                <Button
                    view="outlined"
                    onClick={() => setPaused(p => !p)}
                    title={paused ? 'Resume counter polling' : 'Pause counter polling'}
                >
                    <Icon data={paused ? Play : Pause} size={16} />
                    {paused ? 'Resume' : 'Pause'}
                </Button>
            )}
            {currentConfig && (
                <YamlIO
                    key={currentConfig}
                    configName={currentConfig}
                    rules={rawRules}
                    onImport={handleImportYaml}
                />
            )}
            <Button view="action" onClick={openAdd}>
                <Icon data={Plus} size={16} />
                Add Rule
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
                {draftConfigs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">
                            No ACL configurations found.
                        </div>
                        <Button view="action" onClick={() => setAddConfigOpen(true)}>
                            Add Config
                        </Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            counts={ruleCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={handleTabSelect}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />

                        <div className="fw-content">
                            <RuleTable
                                items={visibleItems}
                                selectedIds={selectedIds}
                                activeRowId={activeRowId}
                                onSelectionChange={setSelectedIds}
                                onEditRule={openEdit}
                                currentIsDirty={currentIsDirty}
                                onSave={handleSavePress}
                                onDiscard={handleDiscard}
                                onDeleteConfig={handleOpenDeleteConfig}
                                rates={rates}
                                enabledCounterNames={enabledCounterNames}
                                onToggleCounter={handleToggleCounter}
                            />
                        </div>
                    </>
                )}

                {selectedIds.size > 0 && (
                    <BulkBar
                        count={selectedIds.size}
                        itemNoun="rule"
                        onDelete={handleOpenBulkDelete}
                        onClear={() => setSelectedIds(new Set())}
                    />
                )}

                <BulkDeleteModal
                    open={Boolean(deleteConfirmOpen && bulkDeleteConfig)}
                    count={bulkDeleteRuleIds.length}
                    itemNoun="rule"
                    configName={bulkDeleteConfig || ''}
                    onClose={handleCloseBulkDelete}
                    onConfirm={handleBulkDelete}
                />

                <AddConfigModal
                    open={addConfigOpen}
                    onClose={() => setAddConfigOpen(false)}
                    onCreate={name => {
                        dispatchDraft({ type: 'ADD_CONFIG', configName: name });
                        updateParams({ [QP_CONFIG]: name });
                        setAddConfigOpen(false);
                    }}
                    placeholder="e.g. acl0"
                    existingNames={draftConfigs}
                />

                <DeleteConfigModal
                    open={Boolean(deleteConfigOpen && deleteConfigTarget)}
                    configName={deleteConfigTarget || ''}
                    onClose={handleCloseDeleteConfig}
                    onConfirm={handleDeleteConfig}
                />

                <RuleDrawer
                    ref={drawerRef}
                    open={drawer.open}
                    mode={drawer.mode}
                    ruleItem={drawer.item}
                    nextIndex={rawRules.length}
                    onClose={closeDrawer}
                    onSave={handleDrawerApply}
                    onDelete={handleDeleteItem}
                    onDuplicate={handleDuplicate}
                />

                {diffModalOpen && (
                    <SaveDiffModal
                        configName={currentConfig}
                        draftRules={rawRules}
                        draftIds={rawIds}
                        serverRules={serverRules(currentConfig)}
                        onClose={() => setDiffModalOpen(false)}
                        onApply={handleSave}
                    />
                )}
            </div>
        </PageLayout>
    );
};

export default AclPage;
