import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Icon, Text } from '@gravity-ui/uikit';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers, usePageKeyboardShortcuts } from '../../../hooks';
import { Funnel, Magnifier, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput } from '../../../components';
import { useForwardDraft } from './useForwardDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { Rule } from '../../../api/forward';
import { ForwardMode } from '../../../api/forward';
import type { RuleItem, RuleDraft } from './types';
import { MODE_LABELS } from './types';
import { ModeFilter } from './ModeFilter';
import type { ModeFilterValue } from './ModeFilter';
import { rulesToNgItems, draftToRule } from './hooks';
import { DRAWER_TRANSITION_MS } from './RuleTable';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useForwardRuleCounters } from './useForwardRuleCounters';
import { AddConfigModal } from '../../_shared/draft';
import { DeleteConfigModal, BulkDeleteModal } from '../../../components';
import { CommandPalette } from '../../_shared/command-palette';
import type { Command, RowAdapter } from '../../_shared/command-palette';
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
        commitDeleteConfig,
        discardConfig,
    } = useForwardDraft();
    const [searchParams, setSearchParams] = useSearchParams();

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
    const [deleteInFlightConfig, setDeleteInFlightConfig] = useState<string | null>(null);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [paletteOpen, setPaletteOpen] = useState(false);
    const [modeFilter, setModeFilter] = useState<ModeFilterValue>('all');
    const [flashRowId, setFlashRowId] = useState<string | null>(null);
    const drawerRef = useRef<RuleDrawerHandle>(null);
    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const search = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);
    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig) || queryConfig === deleteInFlightConfig)) ? queryConfig : (draftConfigs[0] || '');
    const { updateParams, clearConfigParamIfCurrent } = useSearchParamHelpers(setSearchParams, QP_CONFIG);

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

    const { rates } = useForwardRuleCounters(currentConfig, allItems, true);

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

    const handleJumpToRow = useCallback((id: string): void => {
        setFlashRowId(null);
        setTimeout(() => setFlashRowId(id), 0);
    }, []);

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

    useEffect(() => {
        if (!paletteOpen) return;
        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') setPaletteOpen(false);
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [paletteOpen]);

    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                setPaletteOpen((prev) => !prev);
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, []);

    usePageKeyboardShortcuts({
        onNewRule: openAdd,
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add',
                icon: '+',
                label: 'Add rule',
                sub: 'Open the add-rule drawer',
                keywords: 'add rule insert new',
                onSelect: () => { openAdd(); setPaletteOpen(false); },
            },
        ];
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save changes',
                sub: 'Open the diff and save dialog',
                keywords: 'save commit apply',
                onSelect: () => { handleSavePress(); setPaletteOpen(false); },
            });
            list.push({
                id: '__discard',
                icon: '⟲',
                label: 'Discard changes',
                sub: 'Revert to the last saved state',
                keywords: 'discard revert undo reset',
                onSelect: () => { handleDiscard(); setPaletteOpen(false); },
            });
        }
        list.push({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            sub: 'Create a new forward configuration',
            keywords: 'add config create new',
            onSelect: () => { setAddConfigOpen(true); setPaletteOpen(false); },
        });
        if (currentConfig) {
            list.push({
                id: '__delete_config',
                icon: '✕',
                label: 'Delete config',
                sub: `Delete "${currentConfig}"`,
                keywords: 'delete remove config',
                onSelect: () => { setDeleteConfigOpen(true); setPaletteOpen(false); },
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
                onSelect: () => { handleTabSelect(name); setPaletteOpen(false); },
            });
        }
        list.push({
            id: '__filter_in',
            icon: '→',
            label: 'Filter: IN only',
            keywords: 'filter mode in direction',
            onSelect: () => { setModeFilter('in'); setPaletteOpen(false); },
        });
        list.push({
            id: '__filter_out',
            icon: '→',
            label: 'Filter: OUT only',
            keywords: 'filter mode out direction',
            onSelect: () => { setModeFilter('out'); setPaletteOpen(false); },
        });
        list.push({
            id: '__filter_none',
            icon: '→',
            label: 'Filter: NONE only',
            keywords: 'filter mode none direction',
            onSelect: () => { setModeFilter('none'); setPaletteOpen(false); },
        });
        list.push({
            id: '__clear',
            icon: '✕',
            label: 'Clear filters',
            keywords: 'clear reset filter all',
            onSelect: () => { setModeFilter('all'); handleSearchChange(''); setPaletteOpen(false); },
        });
        return list;
    }, [currentIsDirty, currentConfig, draftConfigs, dirtySet, handleTabSelect, openAdd, handleSavePress, handleDiscard, handleSearchChange]);

    const rowAdapter: RowAdapter<RuleItem> = {
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
        onSelect: (id) => { setModeFilter('all'); handleSearchChange(''); handleJumpToRow(id); setPaletteOpen(false); },
        icon: '→',
    };

    const pageHeader = (
        <div className="page-header-bar">
            <Text variant="header-1">Forward</Text>
            <button
                type="button"
                className="cp-trigger"
                onClick={() => setPaletteOpen(true)}
                title="Open command palette (⌘K)"
            >
                <Icon data={Magnifier} size={16} />
                <span className="cp-trigger__placeholder">Search rules or run an action…</span>
                <kbd className="cp-kbd">⌘K</kbd>
            </button>
            <div className="page-header-bar__actions">
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
            </div>
        </div>
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
                    <div className="yn-empty-page">
                        <div className="yn-empty-page__message">
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

                        <div className="fw-toolbar">
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
                            <span className="fw-count">
                                <span style={{ color: 'var(--yn-text)', fontWeight: 600 }}>{visibleItems.length.toLocaleString()}</span>
                                {' / '}{allItems.length.toLocaleString()}
                            </span>
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

                <CommandPalette<RuleItem>
                    open={paletteOpen}
                    onClose={() => setPaletteOpen(false)}
                    placeholder="Search rules or run an action…"
                    commands={commands}
                    rowAdapter={rowAdapter}
                />
            </div>
        </PageLayout>
    );
};

export default ForwardPage;
