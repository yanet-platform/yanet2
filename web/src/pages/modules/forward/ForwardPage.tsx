import React, { useCallback, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Pause, Play, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader } from '../../../components';
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
import './forward.scss';

/** Floating bulk-action bar that appears when rules are selected. */
const BulkBar: React.FC<{
    count: number;
    onDelete: () => void;
    onClear: () => void;
}> = ({ count, onDelete, onClear }) => (
    <div className="fw-bulk-bar">
        <span className="fw-bulk-bar__count">{count} selected</span>
        <button type="button" className="fw-btn fw-btn--danger fw-btn--sm" onClick={onDelete}>
            Delete
        </button>
        <button type="button" className="fw-icon-btn fw-icon-btn--sm" onClick={onClear} aria-label="Clear selection">
            ✕
        </button>
    </div>
);

/** Config tab strip with rule counts and add button. */
const ConfigTabStrip: React.FC<{
    configs: string[];
    activeConfig: string;
    ruleCounts: Map<string, number>;
    dirtyConfigs: Set<string>;
    onSelect: (c: string) => void;
    onAddConfig: () => void;
}> = ({ configs, activeConfig, ruleCounts, dirtyConfigs, onSelect, onAddConfig }) => (
    <div className="fw-tabs" role="tablist">
        {configs.map((cfg) => (
            <button
                key={cfg}
                type="button"
                role="tab"
                aria-selected={cfg === activeConfig}
                className={`fw-tab${cfg === activeConfig ? ' fw-tab--active' : ''}${dirtyConfigs.has(cfg) ? ' fw-tab--dirty' : ''}`}
                onClick={() => onSelect(cfg)}
            >
                <span className="fw-tab__label">{cfg}</span>
                {dirtyConfigs.has(cfg) && (
                    <span className="fw-tab__dot" aria-label="unsaved changes" />
                )}
                <span className="fw-tab__count">{ruleCounts.get(cfg) ?? 0}</span>
            </button>
        ))}
        <Button view="flat" size="s" onClick={onAddConfig} className="fw-tabs__add" title="Add config">
            <Icon data={Plus} size={14} />
        </Button>
    </div>
);

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

    const [activeConfig, setActiveConfig] = useState<string>('');
    const [paused, setPaused] = useState(false);
    const [search, setSearch] = useState('');
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [drawer, setDrawer] = useState<{ open: boolean; mode: 'add' | 'edit'; item: RuleItem | null }>({
        open: false,
        mode: 'add',
        item: null,
    });
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [newConfigName, setNewConfigName] = useState('');
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const searchRef = useRef<HTMLInputElement>(null);
    const drawerRef = useRef<RuleDrawerHandle>(null);

    useUnsavedChangesBlocker(anyDirty);

    const currentConfig = activeConfig || draftConfigs[0] || '';
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

    const handleAddConfig = useCallback((): void => {
        const name = newConfigName.trim();
        if (!name || draftConfigs.includes(name)) return;
        dispatchDraft({ type: 'ADD_CONFIG', configName: name });
        setActiveConfig(name);
        setNewConfigName('');
        setAddConfigOpen(false);
    }, [newConfigName, draftConfigs, dispatchDraft]);

    const handleDeleteConfig = useCallback((): void => {
        dispatchDraft({ type: 'DELETE_CONFIG', configName: currentConfig });
        setActiveConfig('');
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
        setActiveConfig(target);
    }, [currentConfig, dispatchDraft]);

    useKeyboardShortcuts({
        onNewRule: openAdd,
        onFocusSearch: () => searchRef.current?.focus(),
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Forward</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <TextInput
                    controlRef={searchRef}
                    value={search}
                    onUpdate={setSearch}
                    placeholder="Search rules… (/)"
                    startContent={
                        <Flex alignItems="center" justifyContent="center" style={{ paddingInline: 8, color: 'var(--g-color-text-hint)' }}>
                            <Icon data={Magnifier} size={16} />
                        </Flex>
                    }
                    hasClear
                    type="search"
                />
            </div>
            {currentConfig && (
                <YamlIO
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
                        <Button view="action" onClick={() => { setNewConfigName(''); setAddConfigOpen(true); }}>
                            Add Config
                        </Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            ruleCounts={ruleCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={(c) => {
                                setActiveConfig(c);
                                setSelectedIds(new Set());
                                setActiveRowId(null);
                            }}
                            onAddConfig={() => { setNewConfigName(''); setAddConfigOpen(true); }}
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
                        onDelete={() => setDeleteConfirmOpen(true)}
                        onClear={() => setSelectedIds(new Set())}
                    />
                )}

                {deleteConfirmOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setDeleteConfirmOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Delete rules</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setDeleteConfirmOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <p>Delete <strong>{selectedIds.size}</strong> selected rule(s) from <code>{currentConfig}</code>? This cannot be undone.</p>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setDeleteConfirmOpen(false)}>Cancel</button>
                                    <button type="button" className="fw-btn fw-btn--danger" onClick={handleBulkDelete}>
                                        Delete {selectedIds.size} rule(s)
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}

                {addConfigOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setAddConfigOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Add config</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setAddConfigOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <div className="fw-field">
                                    <label className="fw-field__label" htmlFor="fw-new-config-name">
                                        Config name <span className="fw-field__req">*</span>
                                    </label>
                                    <input
                                        id="fw-new-config-name"
                                        className="fw-input"
                                        type="text"
                                        value={newConfigName}
                                        onChange={(e) => setNewConfigName(e.target.value)}
                                        onKeyDown={(e) => {
                                            if (e.key === 'Enter') handleAddConfig();
                                            if (e.key === 'Escape') setAddConfigOpen(false);
                                        }}
                                        placeholder="e.g. default"
                                        autoFocus
                                    />
                                </div>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setAddConfigOpen(false)}>Cancel</button>
                                    <button
                                        type="button"
                                        className="fw-btn fw-btn--primary"
                                        onClick={handleAddConfig}
                                        disabled={!newConfigName.trim() || draftConfigs.includes(newConfigName.trim())}
                                    >
                                        Create
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}

                {deleteConfigOpen && (
                    <div className="fw-modal-backdrop" onClick={() => setDeleteConfigOpen(false)}>
                        <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                            <header className="fw-modal__head">
                                <span className="fw-modal__title">Delete config</span>
                                <button type="button" className="fw-icon-btn" onClick={() => setDeleteConfigOpen(false)} aria-label="Close">✕</button>
                            </header>
                            <div className="fw-modal__body fw-modal__body--confirm">
                                <p>Delete config <code>{currentConfig}</code>? Changes will be applied on Save.</p>
                            </div>
                            <footer className="fw-modal__foot">
                                <span />
                                <div className="fw-modal__foot-actions">
                                    <button type="button" className="fw-btn fw-btn--ghost" onClick={() => setDeleteConfigOpen(false)}>Cancel</button>
                                    <button type="button" className="fw-btn fw-btn--danger" onClick={handleDeleteConfig}>
                                        Mark for deletion
                                    </button>
                                </div>
                            </footer>
                        </div>
                    </div>
                )}

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
