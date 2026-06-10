import React, { useCallback, useMemo, useRef } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Funnel, Plus } from '@gravity-ui/icons';
import { usePageContribution } from '../../../hooks';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, EmptyPagePlaceholder, SearchInput, RowCountDisplay } from '../../../components';
import { usePrefixDraft } from './usePrefixDraft';
import type { PrefixRowItem } from './types';
import { PrefixTable } from './PrefixTable';
import PrefixDrawer from './PrefixDrawer';
import type { PrefixDrawerHandle } from './PrefixDrawer';
import PrefixYamlIO from './PrefixYamlIO';
import { PrefixSaveDiffModal } from './PrefixSaveDiffModal';
import { AddConfigModal, DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '../../../components';
import { useDraftShortcuts, useDraftPageHandlers, useDraftPageState, useDraftPageDerived } from '../../../components/draft';
import { useTabCycle } from '../../_shared/useTabCycle';
import type { Command, RowAdapter, PagePaletteContribution } from '../../../components/command-palette';
import { buildConfigCommands } from '../../../components/command-palette';
import '../../../styles/chrome.scss';
import './decap.scss';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;
const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

const matchesPrefixSearch = (r: PrefixRowItem, q: string): boolean =>
    r.prefix.toLowerCase().includes(q);

const prefixRowsEqual = (s: PrefixRowItem, r: PrefixRowItem): boolean =>
    s.prefix === r.prefix;

const DecapPage: React.FC = () => {
    const {
        draftConfigs, loading, draftRows, serverRows, isDirty, anyDirty,
        dispatchDraft, commitConfig, discardConfig,
    } = usePrefixDraft();

    const pageState = useDraftPageState({
        loading,
        draftConfigs,
        anyDirty,
        configParamKey: QP_CONFIG,
        searchParamKey: QP_SEARCH,
    });
    const {
        search, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        selectedIds, setSelectedIds, deleteConfirmOpen, setDeleteConfirmOpen,
        diffModalOpen, setDiffModalOpen, addConfigOpen, setAddConfigOpen,
        deleteConfigOpen, setDeleteConfigOpen, dragDrop, updateParams,
        setActiveConfig, currentConfig,
    } = pageState;

    const drawerRef = useRef<PrefixDrawerHandle>(null);

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: setActiveConfig,
        enabled: !loading,
    });

    const derived = useDraftPageDerived<PrefixRowItem>({
        pageState,
        draftRows,
        serverRows,
        isDirty,
        draftConfigs,
        loading,
        configParamKey: QP_CONFIG,
        matchesSearch: matchesPrefixSearch,
        rowsEqual: prefixRowsEqual,
    });
    const {
        rawRows, rawServerRows, currentIsDirty, rowCounts: prefixCounts,
        dirtySet, visibleRows, statusById, removedRows,
    } = derived;

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
        list.push(...buildConfigCommands({
            currentConfig,
            draftConfigs,
            dirtySet,
            addConfigSub: 'Create a new decap configuration',
            withKeywords: true,
            onAddConfig: () => setAddConfigOpen(true),
            onDeleteConfig: () => setDeleteConfigOpen(true),
            onSwitchConfig: (name) => setActiveConfig(name),
        }));
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

    const contribution = useMemo<PagePaletteContribution>(() => ({
        commands,
        rowAdapter: rowAdapter as RowAdapter<unknown>,
        placeholder: 'Search prefixes or run an action…',
    }), [commands, rowAdapter]);
    usePageContribution(contribution);

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
                        <div className="yn-toolbar-bordered">
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
                            <RowCountDisplay filtered={visibleRows.length} total={rawRows.length} />
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
