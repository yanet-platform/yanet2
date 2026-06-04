import React, { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Icon, Label } from '@gravity-ui/uikit';
import { Funnel, Pause, Play, Plus } from '@gravity-ui/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder, RowCountDisplay } from '../../../components';
import { useSearchParamHelpers, usePageKeyboardShortcuts, useDirtyConfigSet, useConfigQuerySync } from '../../../hooks';
import { useAclDraft } from './useAclDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { Rule } from '../../../api/acl';
import { ActionKind } from '../../../api/acl';
import type { RuleItem, RuleDraft } from './types';
import { rulesToNgItems, draftToRule } from './hooks';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO, { type ImportMode } from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useAclRuleCounters } from './useAclRuleCounters';
import { AddConfigModal, DRAWER_TRANSITION_MS } from '../../_shared/draft';
import { DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '../../../components';
import { usePalette } from '../../_shared/command-palette';
import type { Command, RowAdapter } from '../../_shared/command-palette';
import { useTabCycle } from '../../_shared/useTabCycle';
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
    const [flashRowId, setFlashRowId] = useState<string | null>(null);
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
    const { updateParams, clearConfigParamIfCurrent } = useSearchParamHelpers(setSearchParams, QP_CONFIG);

    useConfigQuerySync({ currentConfig, loading, queryConfig, paramKey: QP_CONFIG, searchParams, updateParams });

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
        setFlashRowId(null);
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

    const dirtySet = useDirtyConfigSet(draftConfigs, isDirty);

    const deferredSearch = useDeferredValue(search);

    const visibleItems = useMemo((): RuleItem[] => {
        const q = deferredSearch.trim().toLowerCase();
        if (!q) return allItems;
        return allItems.filter(item => item.searchText.includes(q));
    }, [allItems, deferredSearch]);

    const openAdd = useCallback((): void => {
        if (!currentConfig) {
            return;
        }
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: null });
    }, [currentConfig]);

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

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: handleTabSelect,
        enabled: !loading,
    });

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    const handleJumpToRow = useCallback((id: string): void => {
        setFlashRowId(null);
        setTimeout(() => setFlashRowId(id), 0);
    }, []);

    const handleOpenLinkedFwstate = useCallback((): void => {
        if (!currentFwStateName) {
            return;
        }
        navigate(`/modules/fwstate?config=${encodeURIComponent(currentFwStateName)}`);
    }, [currentFwStateName, navigate]);

    usePageKeyboardShortcuts({
        onNewRule: openAdd,
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);

    const { setPageContribution } = usePalette();

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];
        if (currentConfig) {
            list.push({
                id: '__add',
                icon: '+',
                label: 'Add rule',
                sub: 'Open the add-rule drawer',
                keywords: 'add rule insert new',
                onSelect: () => openAdd(),
            });
        }
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save changes',
                sub: 'Open the diff and save dialog',
                keywords: 'save commit apply',
                onSelect: () => handleSavePress(),
            });
            list.push({
                id: '__discard',
                icon: '⟲',
                label: 'Discard changes',
                sub: 'Revert to the last saved state',
                keywords: 'discard revert undo reset',
                onSelect: () => { closeDrawer(); handleDiscard(); },
            });
        }
        list.push({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            sub: 'Create a new ACL configuration',
            keywords: 'add config create new',
            onSelect: () => setAddConfigOpen(true),
        });
        if (currentConfig) {
            list.push({
                id: '__delete_config',
                icon: '✕',
                label: 'Delete config',
                sub: `Delete "${currentConfig}"`,
                keywords: 'delete remove config',
                onSelect: () => handleOpenDeleteConfig(),
            });
        }
        for (const cfg of draftConfigs) {
            if (cfg === currentConfig) continue;
            const name = cfg;
            list.push({
                id: `__config_${name}`,
                icon: '⇥',
                label: `Switch to config ${name}`,
                sub: dirtySet.has(name) ? 'unsaved changes' : undefined,
                keywords: `switch config tab ${name}`,
                onSelect: () => handleTabSelect(name),
            });
        }
        if (enabledCounterNames.size > 0) {
            list.push({
                id: '__pause_resume',
                icon: paused ? '▶' : '⏸',
                label: paused ? 'Resume counters' : 'Pause counters',
                keywords: 'pause resume counter polling',
                onSelect: () => setPaused(p => !p),
            });
        }
        if (currentFwStateName) {
            list.push({
                id: '__open_fwstate',
                icon: '↗',
                label: 'Open linked FWState',
                sub: currentFwStateName,
                keywords: 'fwstate open link navigate',
                onSelect: () => handleOpenLinkedFwstate(),
            });
        }
        list.push({
            id: '__clear_search',
            icon: '✕',
            label: 'Clear search',
            keywords: 'clear reset search filter',
            onSelect: () => handleSearchChange(''),
        });
        return list;
    }, [
        currentIsDirty, currentConfig, draftConfigs, dirtySet,
        enabledCounterNames, paused, currentFwStateName,
        openAdd, handleSavePress, handleDiscard, closeDrawer,
        handleTabSelect, handleOpenDeleteConfig, handleSearchChange, handleOpenLinkedFwstate,
    ]);

    const rowAdapter = useMemo((): RowAdapter<RuleItem> => ({
        rows: allItems,
        getId: (it) => it.id,
        getLabel: (it) => `Rule ${it.index + 1}${it.counter ? ` · ${it.counter}` : ''}`,
        getSub: (it) => {
            const rule = it.rule;
            const devices = (rule.devices ?? []).map(d => d.name ?? '').filter(Boolean);
            return devices.join(', ');
        },
        searchText: (it) => it.searchText,
        onSelect: (id) => { handleSearchChange(''); handleJumpToRow(id); },
        icon: '→',
    }), [allItems, handleSearchChange, handleJumpToRow]);

    useEffect(() => {
        setPageContribution({
            commands,
            rowAdapter: rowAdapter as RowAdapter<unknown>,
            placeholder: 'Search rules or run an action…',
        });
        return () => setPageContribution(null);
    }, [commands, rowAdapter, setPageContribution]);

    const hasStatefulRules = useMemo(() =>
        rawRules.some((rule) => (rule.actions ?? []).some((action) =>
            action.kind === ActionKind.ACTION_KIND_CHECK_STATE || action.kind === ActionKind.ACTION_KIND_CREATE_STATE,
        )), [rawRules]);

    const pageHeader = (
        <CommandPaletteHeader
            title="ACL"
            placeholder="Search rules or run an action…"
            actions={<>
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
                <YamlIO key={currentConfig || '__none'} configName={currentConfig} rules={rawRules} onImport={handleImportYaml} disabled={!currentConfig} />
                <Button view="action" onClick={openAdd}>
                    <Icon data={Plus} size={16} />
                    Add Rule
                </Button>
            </>}
        />
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader} className="yn-flat-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page yn-flat-page">
                {draftConfigs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No ACL configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                    />
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
                        <div className="yn-toolbar-bordered">
                            {currentFwStateName && (
                                <Button size="s" view="outlined" onClick={handleOpenLinkedFwstate}>
                                    FWState: {currentFwStateName}
                                </Button>
                            )}
                            {!currentFwStateName && hasStatefulRules && (
                                <Label theme="warning">Stateful rules without FWState</Label>
                            )}
                            <div style={{ flex: 1 }} />
                            <div style={{ flexBasis: 320, flexShrink: 1 }}>
                                <SearchInput
                                    value={search}
                                    onUpdate={handleSearchChange}
                                    placeholder="Search rules…"
                                    icon={Funnel}
                                    enableFocusShortcut={false}
                                    showShortcutHint={false}
                                />
                            </div>
                            <RowCountDisplay filtered={visibleItems.length} total={allItems.length} />
                        </div>

                        <div className="yn-content">
                            <RuleTable
                                items={visibleItems}
                                selectedIds={selectedIds}
                                activeRowId={activeRowId}
                                flashRowId={flashRowId}
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
