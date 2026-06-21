import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { useConfigListCache, useListNavigation, usePageContribution } from '@yanet/core/hooks';
import { Funnel, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder, RowCountDisplay } from '@yanet/core/components';
import { useForwardDraft } from './useForwardDraft';
import type { Rule } from '@yanet/core/api/forward';
import { ForwardMode } from '@yanet/core/api/forward';
import type { RuleItem, RuleDraft } from './types';
import { MODE_LABELS } from './types';
import { ModeFilter } from './ModeFilter';
import type { ModeFilterValue } from './ModeFilter';
import { rulesToNgItems, draftToRule, itemToDraft } from './hooks';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useForwardRuleCounters } from './useForwardRuleCounters';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '@yanet/core/components';
import { useRulePageState } from '@yanet/core/components/draft';
import type { Command, RowAdapter, PagePaletteContribution } from '@yanet/core/components/command-palette';
import { buildConfigCommands, buildDraftCommands } from '@yanet/core/components/command-palette';
import '@yanet/core/styles/chrome.scss';
import './forward.scss';

const QP_CONFIG = 'config';

const cloneRuleItem = (item: RuleItem): RuleItem => ({ ...item });

const ForwardPage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        loadFailed,
        draftRules,
        serverRules,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    } = useForwardDraft();

    const { configs: cachedConfigs, counts: cachedCounts } = useConfigListCache('forward');

    const [modeFilter, setModeFilter] = useState<ModeFilterValue>('all');

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
        requireConfigForAdd: false,
        clearSelectionOnTabSelect: true,
    });

    const rawRules: Rule[] = draftRules(currentConfig);
    const allItems = useMemo(() => rulesToNgItems(rawRules), [rawRules]);

    const { rates } = useForwardRuleCounters(currentConfig, allItems, true);

    const visibleItems = useMemo((): RuleItem[] => {
        let res = allItems;

        if (modeFilter !== 'all') {
            const modeMap: Record<Exclude<ModeFilterValue, 'all'>, ForwardMode> = {
                in: ForwardMode.IN,
                out: ForwardMode.OUT,
                none: ForwardMode.NONE,
            };
            const targetMode = modeMap[modeFilter];
            res = res.filter((item) => item.mode === targetMode);
        }

        const q = search.trim().toLowerCase();
        if (q) {
            res = res.filter((item) =>
                item.target.toLowerCase().includes(q) ||
                item.counter.toLowerCase().includes(q) ||
                item.deviceNames.some((d) => d.toLowerCase().includes(q)) ||
                item.sourceCidrs.some((s) => s.toLowerCase().includes(q)) ||
                item.dstCidrs.some((s) => s.toLowerCase().includes(q))
            );
        }
        return res;
    }, [allItems, search, modeFilter]);

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

    const handleBulkDelete = useCallback((): void => {
        const indices = visibleItems
            .filter((item) => selectedIds.has(item.id))
            .map((item) => item.index);
        dispatchDraft({ type: 'REMOVE_RULES', configName: currentConfig, indices });
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
    }, [selectedIds, visibleItems, currentConfig, dispatchDraft]);

    const handleDeleteConfig = useCallback(async (): Promise<void> => {
        setDeleteConfigOpen(false);
        setDeleteInFlightConfig(currentConfig);
        const name = currentConfig;
        try {
            await commitDeleteConfig(name);
            clearConfigParamIfCurrent(name);
        } catch {
            // Toast already surfaced by the hook.
        } finally {
            setDeleteInFlightConfig(null);
        }
    }, [currentConfig, commitDeleteConfig, clearConfigParamIfCurrent]);

    const handleImportYaml = useCallback((importedConfigName: string, rules: Rule[]): void => {
        const target = importedConfigName || currentConfig;
        dispatchDraft({ type: 'REPLACE_ALL_RULES', configName: target, rules });
        updateParams({ [QP_CONFIG]: target || null });
    }, [currentConfig, dispatchDraft, updateParams]);

    useEffect(() => {
        setSelectedIds(new Set());
        setActiveRowId(null);
        setDrawer((d) => ({ ...d, open: false, item: null }));
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
        setModeFilter('all');
        setFlashRowId(null);
    }, [currentConfig]);

    const canCreate = !loading && !loadFailed;

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add',
                icon: '+',
                label: 'Add rule',
                sub: 'Open the add-rule drawer',
                keywords: 'add rule insert new',
                onSelect: () => openAdd(),
            },
        ];
        list.push(...buildDraftCommands({
            currentIsDirty,
            onSave: () => handleSavePress(),
            onDiscard: () => handleDiscard(),
        }));
        list.push(...buildConfigCommands({
            currentConfig,
            draftConfigs,
            dirtySet,
            addConfigSub: 'Create a new forward configuration',
            withKeywords: true,
            onAddConfig: () => setAddConfigOpen(true),
            addConfigDisabled: !canCreate,
            onDeleteConfig: () => setDeleteConfigOpen(true),
            onSwitchConfig: (name) => handleTabSelect(name),
        }));
        list.push({
            id: '__filter_in',
            icon: '→',
            label: 'Filter: IN only',
            keywords: 'filter mode in direction',
            onSelect: () => setModeFilter('in'),
        });
        list.push({
            id: '__filter_out',
            icon: '→',
            label: 'Filter: OUT only',
            keywords: 'filter mode out direction',
            onSelect: () => setModeFilter('out'),
        });
        list.push({
            id: '__filter_none',
            icon: '→',
            label: 'Filter: NONE only',
            keywords: 'filter mode none direction',
            onSelect: () => setModeFilter('none'),
        });
        list.push({
            id: '__clear',
            icon: '✕',
            label: 'Clear filters',
            keywords: 'clear reset filter all',
            onSelect: () => { setModeFilter('all'); handleSearchChange(''); },
        });
        return list;
    }, [canCreate, currentIsDirty, currentConfig, draftConfigs, dirtySet, handleTabSelect, openAdd, handleSavePress, handleDiscard, handleSearchChange]);

    const rowAdapter = useMemo((): RowAdapter<RuleItem> => ({
        rows: allItems,
        getId: (it) => it.id,
        getLabel: (it) => it.target || '(no target)',
        getSub: (it) => {
            const parts: string[] = [MODE_LABELS[it.mode]];
            if (it.counter) parts.push(it.counter);
            if (it.deviceNames.length) parts.push(it.deviceNames.join(', '));
            return parts.join(' · ');
        },
        searchText: (it) => [it.target, it.counter, ...it.deviceNames, ...it.sourceCidrs, ...it.dstCidrs].join(' '),
        onSelect: (id) => { setModeFilter('all'); handleSearchChange(''); handleJumpToRow(id); },
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
            title="Forward"
            placeholder="Search rules or run an action…"
            actions={<>
                <YamlIO
                    key={currentConfig || '__none'}
                    configName={currentConfig}
                    rules={rawRules}
                    onImport={handleImportYaml}
                    disabled={!currentConfig}
                />
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
                        message="No forward configurations found."
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
                                    <ModeFilter value={modeFilter} onChange={setModeFilter} />
                                    <div style={{ flex: 1 }} />
                                    <div style={{ flexBasis: 230, flexShrink: 1 }}>
                                        <SearchInput
                                            value={search}
                                            onUpdate={handleSearchChange}
                                            placeholder="Filter rows…"
                                            enableFocusShortcut={false}
                                            showShortcutHint={false}
                                            icon={Funnel}
                                        />
                                    </div>
                                    <RowCountDisplay filtered={visibleItems.length} total={allItems.length} />
                                </div>

                                <div className="yn-content">
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
                                        flashRowId={flashRowId}
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
                    rate={drawer.item ? rates.get(drawer.item.id) : undefined}
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
