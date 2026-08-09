import React, { useCallback, useDeferredValue, useEffect, useMemo, useState } from 'react';
import { Button, Icon, Label } from '@gravity-ui/uikit';
import { Funnel, Pause, Play, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder, RowCountDisplay } from '@yanet/core/components';
import { useConfigListCache, useListNavigation, usePageContribution } from '@yanet/core/hooks';
import { API } from '@yanet/core/api';
import { toaster, compareNatural, validateSyncConfigFormFields } from '@yanet/core/utils';
import { useAclDraft } from './useAclDraft';
import type { Rule } from '@yanet/core/api/acl';
import { ActionKind } from '@yanet/core/api/acl';
import type { RuleItem, RuleDraft } from './types';
import { rulesToNgItems, draftToRule, itemToDraft } from './hooks';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO, { type ImportMode } from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useAclRuleCounters } from './useAclRuleCounters';
import MapSyncPanel from './MapSyncPanel';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '@yanet/core/components';
import { useRulePageState } from '@yanet/core/components/draft';
import type { Command, RowAdapter, PagePaletteContribution } from '@yanet/core/components/command-palette';
import { buildConfigCommands, buildDraftCommands } from '@yanet/core/components/command-palette';
import '@yanet/core/styles/chrome.scss';
import './acl.scss';

const QP_CONFIG = 'config';

const cloneRuleItem = (item: RuleItem): RuleItem => ({ ...item, rule: { ...item.rule } });

const AclPage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        loadFailed,
        draftRules,
        draftRuleIds,
        serverRules,
        draftMapSync,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    } = useAclDraft();

    const canCreate = !loading && !loadFailed;

    const { configs: cachedConfigs, counts: cachedCounts } = useConfigListCache('acl');

    const [paused, setPaused] = useState(false);
    const [enabledCounterNames, setEnabledCounterNames] = useState<Set<string>>(new Set());
    const [deleteConfigTarget, setDeleteConfigTarget] = useState<string | null>(null);
    const [bulkDeleteConfig, setBulkDeleteConfig] = useState<string | null>(null);
    const [bulkDeleteRuleIds, setBulkDeleteRuleIds] = useState<string[]>([]);

    const {
        currentConfig,
        search,
        updateParams,
        clearConfigParamIfCurrent,
        selectedIds,
        setSelectedIds,
        activeRowId,
        setActiveRowId,
        drawer,
        setDrawer,
        deleteConfirmOpen,
        setDeleteConfirmOpen,
        addConfigOpen,
        setAddConfigOpen,
        deleteConfigOpen,
        setDeleteConfigOpen,
        diffModalOpen,
        setDiffModalOpen,
        flashRowId,
        setFlashRowId,
        setDeleteInFlightConfig,
        drawerRef,
        ruleCounts,
        dirtySet,
        currentIsDirty,
        openAdd,
        openEdit,
        closeDrawer,
        handleDrawerApply,
        handleDeleteItem,
        handleDuplicate,
        handleSave,
        handleSavePress,
        handleDiscard,
        handleSearchChange,
        handleJumpToRow,
        handleTabSelect,
    } = useRulePageState<Rule, RuleItem, RuleDraft, RuleDrawerHandle>({
        draftConfigs,
        loading,
        anyDirty,
        isDirty,
        draftRules,
        dispatchDraft,
        saveConfig,
        discardConfig,
        toRule: draftToRule,
        itemToDraft,
        cloneItem: cloneRuleItem,
        requireConfigForAdd: true,
        clearSelectionOnTabSelect: false,
    });

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

    const rawRules: Rule[] = draftRules(currentConfig);
    const rawIds: string[] = draftRuleIds(currentConfig);
    const allItems = useMemo(() => rulesToNgItems(rawRules, rawIds), [rawRules, rawIds]);

    // fwstate-map names for the config-level map selector. Managed by the
    // FWState page; fetched once after the initial config load completes.
    const [maps, setMaps] = useState<string[]>([]);
    useEffect(() => {
        if (loading) return;
        let mounted = true;
        API.fwstate.listMaps()
            .then((res) => {
                if (!mounted) return;
                setMaps((res.maps ?? []).slice().sort((a, b) => compareNatural(a, b)));
            })
            .catch((err) => {
                toaster.error('acl-maps-load', 'Failed to load fwstate-maps', err);
            });
        return () => { mounted = false; };
    }, [loading]);

    const currentMapSync = draftMapSync(currentConfig);
    // CREATE_STATE drives the server-side requirement for map_name + sync_config
    // (the ACL dataplane reads the borrowed maps and sync_config to emit
    // CREATE_STATE sync packets). CHECK_STATE alone does not.
    const hasCreateState = useMemo(
        () => rawRules.some((rule) => (rule.actions ?? []).some((action) =>
            action.kind === ActionKind.ACTION_KIND_CREATE_STATE)),
        [rawRules],
    );
    // The map+sync panel is relevant when a CREATE_STATE rule is present (the
    // fields are required) or when a map is already selected (editable).
    const showMapSync = hasCreateState || Boolean(currentMapSync.mapName);

    const setMapSync = useCallback((next: typeof currentMapSync): void => {
        if (!currentConfig) return;
        dispatchDraft({ type: 'SET_MAP_SYNC', configName: currentConfig, mapSync: next });
    }, [currentConfig, dispatchDraft]);

    const { rates } = useAclRuleCounters(currentConfig, allItems, enabledCounterNames, !paused);

    const deferredSearch = useDeferredValue(search);

    const visibleItems = useMemo((): RuleItem[] => {
        const q = deferredSearch.trim().toLowerCase();
        if (!q) return allItems;
        return allItems.filter(item => item.searchText.includes(q));
    }, [allItems, deferredSearch]);

    const navRows = useMemo(() => visibleItems.map((it) => ({ id: it.id })), [visibleItems]);
    useListNavigation({
        rows: navRows,
        activeId: activeRowId,
        setActiveId: setActiveRowId,
        onActivate: (row) => {
            const it = visibleItems.find((i) => i.id === row.id);
            if (it) openEdit(it);
        },
        onDelete: (row) => {
            const it = visibleItems.find((i) => i.id === row.id);
            if (it) handleDeleteItem(it);
        },
        enabled: !drawer.open,
    });

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

    // Validate config-level map + sync before opening the save-diff modal.
    //
    // map_name + sync_config are required iff any rule uses CREATE_STATE and
    // forbidden otherwise (the save path drops them when not needed). Surface
    // the requirement here so the user gets a clear error before the server
    // rejects the UpdateConfig call.
    const handleSavePressValidated = useCallback((): void => {
        if (hasCreateState) {
            if (!currentMapSync.mapName) {
                toaster.error('acl-save-validate', 'A +state rule requires a fwstate-map. Select one under "FWState map & sync".');
                return;
            }
            if (!validateSyncConfigFormFields(currentMapSync.sync)) {
                toaster.error('acl-save-validate', 'Invalid FWState sync fields. Check addresses, ports, and timeouts.');
                return;
            }
        }
        handleSavePress();
    }, [currentMapSync, hasCreateState, handleSavePress]);

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
        list.push(...buildDraftCommands({
            currentIsDirty,
            onSave: () => handleSavePressValidated(),
            onDiscard: () => { closeDrawer(); handleDiscard(); },
        }));
        list.push(...buildConfigCommands({
            currentConfig,
            draftConfigs,
            dirtySet,
            addConfigSub: 'Create a new ACL configuration',
            withKeywords: true,
            onAddConfig: () => setAddConfigOpen(true),
            addConfigDisabled: !canCreate,
            onDeleteConfig: () => handleOpenDeleteConfig(),
            onSwitchConfig: (name) => handleTabSelect(name),
        }));
        if (enabledCounterNames.size > 0) {
            list.push({
                id: '__pause_resume',
                icon: paused ? '▶' : '⏸',
                label: paused ? 'Resume counters' : 'Pause counters',
                keywords: 'pause resume counter polling',
                onSelect: () => setPaused(p => !p),
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
        loading, currentIsDirty, currentConfig, draftConfigs, dirtySet,
        enabledCounterNames, paused,
        openAdd, handleSavePressValidated, handleDiscard, closeDrawer,
        handleTabSelect, handleOpenDeleteConfig, handleSearchChange,
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

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        rowAdapter: rowAdapter as RowAdapter<unknown>,
        placeholder: 'Search rules or run an action…',
    }), [commands, rowAdapter]);
    usePageContribution(contribution);

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

    // While a warm cache exists, keep the tab strip mounted from cached names
    // and counts so it does not blink on remount; only the rows below reload.
    const tabConfigs = loading ? cachedConfigs : draftConfigs;
    const tabCounts = loading ? cachedCounts : ruleCounts;

    if (loading && cachedConfigs.length === 0) {
        return (
            <PageLayout header={pageHeader} className="yn-flat-layout">
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page yn-flat-page">
                {tabConfigs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No ACL configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                        actionDisabled={!canCreate}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={tabConfigs}
                            activeConfig={currentConfig}
                            counts={tabCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={handleTabSelect}
                            onAddConfig={() => setAddConfigOpen(true)}
                            addConfigDisabled={!canCreate}
                        />
                        {loading ? (
                            <PageLoader loading size="l" />
                        ) : (
                            <>
                                <div className="yn-toolbar-bordered">
                                    {hasCreateState && !currentMapSync.mapName && (
                                        <Label theme="danger">+state rule needs a fwstate-map</Label>
                                    )}
                                    {currentMapSync.mapName && (
                                        <Label theme="success" size="s">map: {currentMapSync.mapName}</Label>
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

                                {showMapSync && (
                                    <MapSyncPanel
                                        value={currentMapSync}
                                        maps={maps}
                                        required={hasCreateState}
                                        onChange={setMapSync}
                                    />
                                )}

                                <div className="yn-content">
                                    <RuleTable
                                        items={visibleItems}
                                        selectedIds={selectedIds}
                                        activeRowId={activeRowId}
                                        flashRowId={flashRowId}
                                        onSelectionChange={setSelectedIds}
                                        onEditRule={openEdit}
                                        currentIsDirty={currentIsDirty}
                                        onSave={handleSavePressValidated}
                                        onDiscard={handleDiscard}
                                        onDeleteConfig={handleOpenDeleteConfig}
                                        rates={rates}
                                        enabledCounterNames={enabledCounterNames}
                                        onToggleCounter={handleToggleCounter}
                                    />
                                </div>
                            </>
                        )}
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
