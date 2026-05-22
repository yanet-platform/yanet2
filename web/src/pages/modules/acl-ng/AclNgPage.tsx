import React, { useCallback, useMemo, useRef, useState } from 'react';
import { Button, Flex, Icon, Text, TextInput } from '@gravity-ui/uikit';
import { Magnifier, Pause, Play, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { useAclNgDraft } from './useAclNgDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { Rule } from '../../../api/acl-ng';
import type { RuleItem, RuleDraft } from './types';
import { rulesToNgItems, expandRuleItem, draftToRule, useKeyboardShortcuts } from './hooks';
import { DRAWER_TRANSITION_MS } from './RuleTable';
import RuleTable from './RuleTable';
import RuleDrawer from './RuleDrawer';
import type { RuleDrawerHandle } from './RuleDrawer';
import YamlIO, { type ImportMode } from './YamlIO';
import { SaveDiffModal } from './SaveDiffModal';
import { useAclNgRuleCounters } from './useAclNgRuleCounters';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal } from '../../_shared/draft';
import '../../../styles/draft-page.scss';
import './acl-ng.scss';

const AclNgPage: React.FC = () => {
    const {
        draftConfigs,
        loading,
        draftRules,
        draftRuleIds,
        serverRules,
        isDirty,
        anyDirty,
        dispatchDraft,
        saveConfig,
        commitDeleteConfig,
        discardConfig,
    } = useAclNgDraft();

    const [activeConfig, setActiveConfig] = useState<string>('');
    const [paused, setPaused] = useState(false);
    const [enabledCounterNames, setEnabledCounterNames] = useState<Set<string>>(new Set());
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
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const searchRef = useRef<HTMLInputElement>(null);
    const drawerRef = useRef<RuleDrawerHandle>(null);

    useUnsavedChangesBlocker(anyDirty);

    const currentConfig = activeConfig || draftConfigs[0] || '';
    const rawRules: Rule[] = draftRules(currentConfig);
    const rawIds: string[] = draftRuleIds(currentConfig);
    const allItems = useMemo(() => rulesToNgItems(rawRules, rawIds), [rawRules, rawIds]);

    const { rates } = useAclNgRuleCounters(currentConfig, allItems, enabledCounterNames, !paused);

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
        return allItems.filter(item => {
            // Cheap checks first (no CIDR decode).
            if (item.counter.toLowerCase().includes(q)) return true;
            // Decode CIDRs and device names only when needed.
            const expanded = expandRuleItem(item.rule);
            return (
                expanded.sourceCidrs.some(s => s.toLowerCase().includes(q)) ||
                expanded.dstCidrs.some(s => s.toLowerCase().includes(q)) ||
                expanded.deviceNames.some(d => d.toLowerCase().includes(q))
            );
        });
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

    const handleBulkDelete = useCallback((): void => {
        const indices = visibleItems
            .filter(item => selectedIds.has(item.id))
            .map(item => item.index);
        dispatchDraft({ type: 'REMOVE_RULES', configName: currentConfig, indices });
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
    }, [selectedIds, visibleItems, currentConfig, dispatchDraft]);

    const handleDeleteConfig = useCallback(async (): Promise<void> => {
        const name = currentConfig;
        setDeleteConfigOpen(false);
        try {
            await commitDeleteConfig(name);
            setActiveConfig('');
        } catch {
            // Toast already surfaced by the hook.
        }
    }, [currentConfig, commitDeleteConfig]);

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
        setActiveConfig(target);
    }, [currentConfig, draftRules, dispatchDraft]);

    useKeyboardShortcuts({
        onNewRule: openAdd,
        onFocusSearch: () => searchRef.current?.focus(),
        onEscape: closeDrawer,
        drawerOpen: drawer.open,
    });

    const currentIsDirty = isDirty(currentConfig);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">ACL NG</Text>
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
                            No ACL NG configurations found.
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
                            onSelect={c => {
                                setActiveConfig(c);
                                setSelectedIds(new Set());
                                setActiveRowId(null);
                            }}
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
                                onDeleteConfig={() => setDeleteConfigOpen(true)}
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
                    onCreate={name => {
                        dispatchDraft({ type: 'ADD_CONFIG', configName: name });
                        setActiveConfig(name);
                        setAddConfigOpen(false);
                    }}
                    placeholder="e.g. acl0"
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

export default AclNgPage;
