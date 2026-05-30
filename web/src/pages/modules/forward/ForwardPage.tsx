import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { useSearchParams } from 'react-router-dom';
import { Pause, Play, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput } from '../../../components';
import { useForwardDraft } from './useForwardDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { Rule } from '../../../api/forward';
import type { RuleItem, RuleDraft } from './types';
import { rulesToNgItems, draftToRule, useKeyboardShortcuts } from './hooks';
import { DRAWER_TRANSITION_MS } from './RuleTable';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useForwardRuleCounters } from './useForwardRuleCounters';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal } from '../../_shared/draft';
import '../../../styles/draft-page.scss';
import './forward.scss';

const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

const ForwardPage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        draftRules,
        serverRules,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        discardConfig,
    } = useForwardDraft();
    const [searchParams, setSearchParams] = useSearchParams();

    const [paused, setPaused] = useState(false);
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
    const drawerRef = useRef<RuleDrawerHandle>(null);
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const search = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);
    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig))) ? queryConfig : (draftConfigs[0] || '');
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

    useUnsavedChangesBlocker(anyDirty);

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

    const rawRules: Rule[] = draftRules(currentConfig);
    const allItems = useMemo(() => rulesToNgItems(rawRules), [rawRules]);

    const { rates } = useForwardRuleCounters(currentConfig, allItems, !paused);

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

    const visibleItems = useMemo((): RuleItem[] => {
        const q = search.trim().toLowerCase();
        if (!q) return allItems;
        return allItems.filter((item) =>
            item.target.toLowerCase().includes(q) ||
            item.counter.toLowerCase().includes(q) ||
            item.deviceNames.some((d) => d.toLowerCase().includes(q)) ||
            item.sourceCidrs.some((s) => s.toLowerCase().includes(q)) ||
            item.dstCidrs.some((s) => s.toLowerCase().includes(q))
        );
    }, [allItems, search]);

    const openAdd = useCallback((): void => {
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: null });
    }, []);

    const openEdit = useCallback((item: RuleItem): void => {
        setActiveRowId(item.id);
        setDrawer({ open: true, mode: 'edit', item });
    }, []);

    const closeDrawer = useCallback((): void => {
        setDrawer((d) => ({ ...d, open: false }));
        setTimeout(() => {
            setActiveRowId(null);
            setDrawer((d) => ({ ...d, item: null }));
        }, DRAWER_TRANSITION_MS);
    }, []);

    /** Apply a rule draft to local state only; no API call. */
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
        setDrawer({ open: true, mode: 'add', item: { ...item } });
    }, []);

    const handleBulkDelete = useCallback((): void => {
        const indices = visibleItems
            .filter((item) => selectedIds.has(item.id))
            .map((item) => item.index);
        dispatchDraft({ type: 'REMOVE_RULES', configName: currentConfig, indices });
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
    }, [selectedIds, visibleItems, currentConfig, dispatchDraft]);

    const handleDeleteConfig = useCallback((): void => {
        dispatchDraft({ type: 'DELETE_CONFIG', configName: currentConfig });
        setDeleteConfigOpen(false);
    }, [currentConfig, dispatchDraft]);

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

    const handleDiscard = useCallback((): void => {
        discardConfig(currentConfig);
    }, [currentConfig, discardConfig]);

    const handleImportYaml = useCallback((importedConfigName: string, rules: Rule[]): void => {
        const target = importedConfigName || currentConfig;
        dispatchDraft({ type: 'REPLACE_ALL_RULES', configName: target, rules });
        updateParams({ [QP_CONFIG]: target || null });
    }, [currentConfig, dispatchDraft, updateParams]);

    const handleTabSelect = useCallback((cfg: string): void => {
        updateParams({ [QP_CONFIG]: cfg || null });
        setSelectedIds(new Set());
        setActiveRowId(null);
    }, [updateParams]);

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    useEffect(() => {
        setSelectedIds(new Set());
        setActiveRowId(null);
        setDrawer((d) => ({ ...d, open: false, item: null }));
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
    }, [currentConfig]);

    useKeyboardShortcuts({
        onNewRule: openAdd,
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Forward</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <SearchInput
                    value={search}
                    onUpdate={handleSearchChange}
                    placeholder="Search rules…"
                />
            </div>
            {currentConfig && (
                <YamlIO
                    key={currentConfig}
                    configName={currentConfig}
                    rules={rawRules}
                    onImport={handleImportYaml}
                />
            )}
            <Button
                view="flat"
                size="m"
                onClick={() => setPaused(p => !p)}
                title={paused ? 'Resume counters' : 'Pause counters'}
            >
                <Icon data={paused ? Play : Pause} size={16} />
                {paused ? 'Resume counters' : 'Pause counters'}
            </Button>
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
                            No forward configurations found.
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
                                rateValues={rates}
                                onSelectionChange={setSelectedIds}
                                onEditRule={openEdit}
                                currentIsDirty={currentIsDirty}
                                onSave={handleSavePress}
                                onDiscard={handleDiscard}
                                onDeleteConfig={() => setDeleteConfigOpen(true)}
                            />
                        </div>
                    </>
                )}

                {selectedIds.size > 0 && (
                    <BulkBar
                        count={selectedIds.size}
                        itemNoun="rule"
                        onDelete={() => setDeleteConfirmOpen(true)}
                        onClear={() => setSelectedIds(new Set())}
                    />
                )}

                <BulkDeleteModal
                    open={deleteConfirmOpen}
                    count={selectedIds.size}
                    itemNoun="rule"
                    configName={currentConfig}
                    onClose={() => setDeleteConfirmOpen(false)}
                    onConfirm={handleBulkDelete}
                />

                <AddConfigModal
                    open={addConfigOpen}
                    onClose={() => setAddConfigOpen(false)}
                    onCreate={(name) => {
                        dispatchDraft({ type: 'ADD_CONFIG', configName: name });
                        updateParams({ [QP_CONFIG]: name });
                        setAddConfigOpen(false);
                    }}
                    placeholder="e.g. default"
                    existingNames={draftConfigs}
                />

                <DeleteConfigModal
                    open={deleteConfigOpen}
                    configName={currentConfig}
                    onClose={() => setDeleteConfigOpen(false)}
                    onConfirm={handleDeleteConfig}
                />

                <RuleDrawer
                    ref={drawerRef}
                    open={drawer.open}
                    mode={drawer.mode}
                    ruleItem={drawer.item}
                    onClose={closeDrawer}
                    onSave={handleDrawerApply}
                    onDelete={handleDeleteItem}
                    onDuplicate={handleDuplicate}
                />

                {diffModalOpen && (
                    <SaveDiffModal
                        configName={currentConfig}
                        draftRules={rawRules}
                        serverRules={serverRules(currentConfig)}
                        onClose={() => setDiffModalOpen(false)}
                        onApply={handleSave}
                    />
                )}
            </div>
        </PageLayout>
    );
};

export default ForwardPage;
