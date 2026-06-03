import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, Icon } from '@gravity-ui/uikit';
import { Funnel, Plus } from '@gravity-ui/icons';
import { useSearchParamHelpers } from '../../../hooks';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, EmptyPagePlaceholder, SearchInput } from '../../../components';
import { usePrefixDraft } from './usePrefixDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { PrefixRowItem } from './types';
import { PrefixTable } from './PrefixTable';
import PrefixDrawer from './PrefixDrawer';
import type { PrefixDrawerHandle } from './PrefixDrawer';
import PrefixYamlIO from './PrefixYamlIO';
import { PrefixSaveDiffModal } from './PrefixSaveDiffModal';
import {
    AddConfigModal,
    useDraftShortcuts, useDraftDragDrop, useDraftPageHandlers,
} from '../../_shared/draft';
import { DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '../../../components';
import { useTabCycle } from '../../_shared/useTabCycle';
import { usePalette } from '../../_shared/command-palette';
import type { Command, RowAdapter } from '../../_shared/command-palette';
import '../../../styles/draft-page.scss';
import './decap.scss';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;
const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

const DecapPage: React.FC = () => {
    const {
        draftConfigs, loading, draftRows, serverRows, isDirty, anyDirty,
        dispatchDraft, commitConfig, discardConfig,
    } = usePrefixDraft();
    const [searchParams, setSearchParams] = useSearchParams();

    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const search = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);

    const drawerRef = useRef<PrefixDrawerHandle>(null);
    const dragDrop = useDraftDragDrop();
    const { handleDragLeave } = dragDrop;

    useUnsavedChangesBlocker(anyDirty);

    const { updateParams } = useSearchParamHelpers(setSearchParams);

    const setActiveConfig = useCallback((configName: string): void => {
        updateParams({ [QP_CONFIG]: configName || null });
    }, [updateParams]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig))) ? queryConfig : (draftConfigs[0] || '');

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: setActiveConfig,
        enabled: !loading,
    });

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

    useEffect(() => {
        setActiveRowId(null);
        setEditingRowId(null);
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
        handleDragLeave();
    }, [currentConfig, handleDragLeave]);

    const { setPageContribution } = usePalette();

    const rawRows: PrefixRowItem[] = draftRows(currentConfig);
    const rawServerRows: PrefixRowItem[] = serverRows(currentConfig);
    const currentIsDirty = isDirty(currentConfig);

    const prefixCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach((c) => m.set(c, draftRows(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRows]);

    const dirtySet = useMemo((): Set<string> => {
        const s = new Set<string>();
        draftConfigs.forEach((c) => { if (isDirty(c)) s.add(c); });
        return s;
    }, [draftConfigs, isDirty]);

    const visibleRows = useMemo((): PrefixRowItem[] => {
        const q = search.trim().toLowerCase();
        if (!q) return rawRows;
        return rawRows.filter((r) => r.prefix.toLowerCase().includes(q));
    }, [rawRows, search]);

    const statusById = useMemo((): Map<string, import('./types').PrefixRowStatus> => {
        const m = new Map<string, import('./types').PrefixRowStatus>();
        const serverById = new Map(rawServerRows.map((r) => [r.id, r]));
        for (const r of rawRows) {
            const s = serverById.get(r.id);
            if (!s) m.set(r.id, 'added');
            else m.set(r.id, s.prefix === r.prefix ? 'same' : 'changed');
        }
        return m;
    }, [rawRows, rawServerRows]);

    const removedRows = useMemo((): PrefixRowItem[] => {
        const localIds = new Set(rawRows.map((r) => r.id));
        return rawServerRows.filter((r) => !localIds.has(r.id));
    }, [rawRows, rawServerRows]);

    const editingIndex = editingRowId ? rawRows.findIndex((r) => r.id === editingRowId) : -1;
    const editingRow = editingIndex >= 0 ? rawRows[editingIndex] : null;

    const handlers = useDraftPageHandlers<PrefixRowItem>({
        currentConfig, rawRows, editingIndex, activeRowId, editingRowId, selectedIds,
        dispatchDraft, commitConfig, discardConfig,
        drawerFlush: () => drawerRef.current?.flushAndApply(),
        setActiveConfig, setActiveRowId, setEditingRowId, setSelectedIds,
        setDiffModalOpen, setDeleteConfirmOpen, setDeleteConfigOpen,
        dragDrop,
    });

    const openAdd = useCallback((): void => {
        const newRow: PrefixRowItem = { id: makeRowId(), prefix: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    }, [currentConfig, dispatchDraft, setActiveRowId, setEditingRowId]);

    useDraftShortcuts({
        rows: rawRows, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        onDeleteRow: handlers.handleDeleteRow,
    });

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add',
                icon: '+',
                label: 'Add prefix',
                sub: 'Open the add-prefix drawer',
                keywords: 'add prefix insert new',
                onSelect: () => openAdd(),
            },
        ];
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save changes',
                sub: 'Open the diff and save dialog',
                keywords: 'save commit apply',
                onSelect: () => handlers.handleCommitPress(),
            });
            list.push({
                id: '__discard',
                icon: '⟲',
                label: 'Discard changes',
                sub: 'Revert to the last saved state',
                keywords: 'discard revert undo reset',
                onSelect: () => handlers.handleDiscard(),
            });
        }
        list.push({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            sub: 'Create a new decap configuration',
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
                onSelect: () => setDeleteConfigOpen(true),
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
                onSelect: () => setActiveConfig(name),
            });
        }
        list.push({
            id: '__clear_search',
            icon: '✕',
            label: 'Clear search',
            keywords: 'clear reset search filter',
            onSelect: () => updateParams({ [QP_SEARCH]: null }),
        });
        return list;
    }, [
        currentIsDirty, currentConfig, draftConfigs, dirtySet,
        openAdd, handlers, setAddConfigOpen, setDeleteConfigOpen, setActiveConfig, updateParams,
    ]);

    const rowAdapter = useMemo((): RowAdapter<PrefixRowItem> => ({
        rows: rawRows,
        getId: (r) => r.id,
        getLabel: (r) => r.prefix || '(empty)',
        searchText: (r) => r.prefix,
        onSelect: (id) => {
            updateParams({ [QP_SEARCH]: null });
            setActiveRowId(id);
            setEditingRowId(id);
        },
        icon: '→',
    }), [rawRows, updateParams, setActiveRowId, setEditingRowId]);

    useEffect(() => {
        setPageContribution({
            commands,
            rowAdapter: rowAdapter as RowAdapter<unknown>,
            placeholder: 'Search prefixes or run an action…',
        });
        return () => setPageContribution(null);
    }, [commands, rowAdapter, setPageContribution]);

    const pageHeader = (
        <CommandPaletteHeader
            title="Decap"
            placeholder="Search prefixes or run an action…"
            actions={<>
                <PrefixYamlIO key={currentConfig || '__none'} configName={currentConfig} rows={rawRows} onImport={handlers.handleImportYaml} disabled={!currentConfig} />
                <Button view="action" onClick={openAdd}>
                    <Icon data={Plus} size={16} />
                    Add Prefix
                </Button>
            </>}
        />
    );

    if (loading) return <PageLayout header={pageHeader} className="yn-flat-layout"><PageLoader loading size="l" /></PageLayout>;

    return (
        <PageLayout header={pageHeader} className="yn-flat-layout">
            <div className="yn-page yn-flat-page">
                {draftConfigs.length === 0 ? (
                    <EmptyPagePlaceholder
                        message="No decap configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            counts={prefixCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={setActiveConfig}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />
                        <div className="decap-toolbar">
                            <div style={{ flex: 1 }} />
                            <div style={{ flexBasis: 230, flexShrink: 1 }}>
                                <SearchInput
                                    value={search}
                                    onUpdate={(value) => updateParams({ [QP_SEARCH]: value || null })}
                                    placeholder="Filter prefixes…"
                                    enableFocusShortcut={false}
                                    showShortcutHint={false}
                                    icon={Funnel}
                                />
                            </div>
                            <span className="decap-count">
                                <span style={{ color: 'var(--yn-text)', fontWeight: 600 }}>{visibleRows.length.toLocaleString()}</span>
                                {' / '}{rawRows.length.toLocaleString()}
                            </span>
                        </div>
                        <div className="yn-content">
                            <PrefixTable
                                allRows={rawRows}
                                visibleRows={visibleRows}
                                statusById={statusById}
                                removedRows={search ? [] : removedRows}
                                activeRowId={activeRowId}
                                editingRowId={editingRowId}
                                selectedIds={selectedIds}
                                dragOverState={dragDrop.dragOverState}
                                onRowClick={setActiveRowId}
                                onEditRow={(id) => { setActiveRowId(id); setEditingRowId(id); }}
                                onRestoreRow={handlers.handleRestoreRow}
                                onSelectionChange={setSelectedIds}
                                onDragStart={dragDrop.handleDragStart}
                                onDragOver={dragDrop.handleDragOver}
                                onDragLeave={dragDrop.handleDragLeave}
                                onDrop={handlers.handleDrop}
                                currentIsDirty={currentIsDirty}
                                onSave={handlers.handleCommitPress}
                                onDiscard={handlers.handleDiscard}
                                onDeleteConfig={() => setDeleteConfigOpen(true)}
                            />
                        </div>
                    </>
                )}

                {selectedIds.size > 0 && (
                    <BulkBar count={selectedIds.size} itemNoun="prefix" onDelete={() => setDeleteConfirmOpen(true)} onClear={() => setSelectedIds(new Set())} />
                )}

                <BulkDeleteModal open={deleteConfirmOpen} count={selectedIds.size} itemNoun="prefix" configName={currentConfig} onClose={() => setDeleteConfirmOpen(false)} onConfirm={handlers.handleBulkDelete} />

                <PrefixDrawer ref={drawerRef} open={!!editingRow} row={editingRow} index={editingIndex} total={rawRows.length} onClose={handlers.closeDrawer} onChange={handlers.handleRowChange} onDelete={handlers.handleDeleteRow} onJump={handlers.handleJumpEdit} />

                {diffModalOpen && (
                    <PrefixSaveDiffModal configName={currentConfig} draftRows={rawRows} serverRows={rawServerRows} onClose={() => setDiffModalOpen(false)} onApply={handlers.handleCommit} />
                )}

                <AddConfigModal open={addConfigOpen} onClose={() => setAddConfigOpen(false)} onCreate={(name) => { dispatchDraft({ type: 'ADD_CONFIG', configName: name }); setActiveConfig(name); setAddConfigOpen(false); }} title="Add decap config" placeholder="e.g. dec0" existingNames={draftConfigs} />

                <DeleteConfigModal open={deleteConfigOpen} configName={currentConfig} onClose={() => setDeleteConfigOpen(false)} onConfirm={handlers.handleDeleteConfig} />
            </div>
        </PageLayout>
    );
};

export default DecapPage;
