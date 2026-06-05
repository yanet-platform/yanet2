import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers, useDirtyConfigSet, useConfigQuerySync } from '../../../hooks';
import { Funnel, Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar, SearchInput, EmptyPagePlaceholder, RowCountDisplay } from '../../../components';
import { useFIBDraft } from './useFIBDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { FIBRowItem } from './types';
import { FIBTable } from './FIBTable';
import FIBDrawer from './FIBDrawer';
import type { FIBDrawerHandle } from './FIBDrawer';
import FIBYamlIO from './FIBYamlIO';
import { FIBSaveDiffModal } from './FIBSaveDiffModal';
import {
    AddConfigModal, isValidCIDR, useDraftShortcuts, useDraftDragDrop, useDraftPageHandlers, computeRowStatuses,
} from '../../_shared/draft';
import { DeleteConfigModal, BulkDeleteModal, CommandPaletteHeader } from '../../../components';
import { usePalette } from '../../../components/command-palette';
import type { Command, RowAdapter } from '../../../components/command-palette';
import { useTabCycle } from '../../_shared/useTabCycle';
import '../../../styles/draft-page.scss';
import './route.scss';

const QP_CONFIG = 'config';
const QP_SEARCH = 'search';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;

const RoutePage: React.FC = () => {
    const {
        draftConfigs, loading, draftRows, serverRows, isDirty, anyDirty,
        dispatchDraft, commitConfig, discardConfig,
    } = useFIBDraft();
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
    const drawerRef = useRef<FIBDrawerHandle>(null);
    const dragDrop = useDraftDragDrop();
    const { handleDragLeave } = dragDrop;

    useUnsavedChangesBlocker(anyDirty);

    const { updateParams } = useSearchParamHelpers(setSearchParams);

    const setActiveConfig = useCallback((configName: string): void => {
        updateParams({ [QP_CONFIG]: configName || null });
    }, [updateParams]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig))) ? queryConfig : (draftConfigs[0] || '');

    useConfigQuerySync({ currentConfig, loading, queryConfig, paramKey: QP_CONFIG, searchParams, updateParams });

    useEffect(() => {
        setActiveRowId(null);
        setEditingRowId(null);
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
        handleDragLeave();
    }, [currentConfig, handleDragLeave]);

    const rawRows: FIBRowItem[] = draftRows(currentConfig);
    const rawServerRows: FIBRowItem[] = serverRows(currentConfig);
    const currentIsDirty = isDirty(currentConfig);

    const routeCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach((c) => m.set(c, draftRows(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRows]);

    const dirtySet = useDirtyConfigSet(draftConfigs, isDirty);

    const visibleRows = useMemo((): FIBRowItem[] => {
        const q = search.trim().toLowerCase();
        if (!q) return rawRows;
        return rawRows.filter((r) =>
            r.prefix.toLowerCase().includes(q) ||
            r.dst_mac.toLowerCase().includes(q) ||
            r.src_mac.toLowerCase().includes(q) ||
            r.device.toLowerCase().includes(q),
        );
    }, [rawRows, search]);

    const { statusById, removedRows } = useMemo(
        () => computeRowStatuses(
            rawRows, rawServerRows,
            (s, r) => s.prefix === r.prefix && s.dst_mac === r.dst_mac && s.src_mac === r.src_mac && s.device === r.device,
        ),
        [rawRows, rawServerRows],
    );

    const editingIndex = editingRowId ? rawRows.findIndex((r) => r.id === editingRowId) : -1;
    const editingRow = editingIndex >= 0 ? rawRows[editingIndex] : null;

    const handlers = useDraftPageHandlers<FIBRowItem>({
        currentConfig, rawRows, editingIndex, activeRowId, editingRowId, selectedIds,
        dispatchDraft, commitConfig, discardConfig,
        drawerFlush: () => drawerRef.current?.flushAndApply(),
        setActiveConfig, setActiveRowId, setEditingRowId, setSelectedIds,
        setDiffModalOpen, setDeleteConfirmOpen, setDeleteConfigOpen,
        dragDrop,
    });

    const { setPageContribution } = usePalette();

    const openAdd = useCallback((prefix = ''): void => {
        const newRow: FIBRowItem = { id: makeRowId(), prefix, dst_mac: '', src_mac: '', device: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    }, [currentConfig, dispatchDraft]);

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    const handleConfigSelect = useCallback((cfg: string): void => {
        setActiveConfig(cfg);
    }, [setActiveConfig]);

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: handleConfigSelect,
        enabled: !loading,
    });

    useDraftShortcuts({
        rows: rawRows, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        onDeleteRow: handlers.handleDeleteRow,
    });

    const commands = useMemo((): Command[] => {
        const list: Command[] = [
            {
                id: '__add',
                icon: '+',
                label: 'Add route',
                keywords: 'add route insert new',
                onSelect: () => openAdd(),
            },
        ];
        if (currentIsDirty) {
            list.push({
                id: '__save',
                icon: '✓',
                label: 'Save changes',
                onSelect: () => handlers.handleCommitPress(),
            });
            list.push({
                id: '__discard',
                icon: '⟲',
                label: 'Discard changes',
                onSelect: () => handlers.handleDiscard(),
            });
        }
        list.push({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            onSelect: () => setAddConfigOpen(true),
        });
        if (currentConfig) {
            list.push({
                id: '__delete_config',
                icon: '✕',
                label: 'Delete config',
                sub: `Delete "${currentConfig}"`,
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
                onSelect: () => handleConfigSelect(name),
            });
        }
        if (search) {
            list.push({
                id: '__clear',
                icon: '✕',
                label: 'Clear search',
                keywords: 'clear reset search',
                onSelect: () => handleSearchChange(''),
            });
        }
        return list;
    }, [currentIsDirty, currentConfig, draftConfigs, dirtySet, search, handlers, handleConfigSelect, handleSearchChange, openAdd]);

    const dynamicCommands = useCallback((q: string): Command[] => {
        if (isValidCIDR(q.trim())) {
            return [
                {
                    id: '__add_cidr',
                    icon: '⌖',
                    label: `Add route for ${q.trim()}`,
                    sub: 'Pre-fill a new route with this prefix',
                    onSelect: () => openAdd(q.trim()),
                },
            ];
        }
        return [];
    }, [openAdd]);

    const rowAdapter = useMemo((): RowAdapter<FIBRowItem> => ({
        rows: rawRows,
        getId: (r) => r.id,
        getLabel: (r) => r.prefix || '(no prefix)',
        getSub: (r) => `${r.dst_mac || '—'} · ${r.device || '—'}`,
        searchText: (r) => [r.prefix, r.dst_mac, r.src_mac, r.device].join(' '),
        onSelect: (id) => { setActiveRowId(id); setEditingRowId(id); },
        icon: '→',
        max: 7,
    }), [rawRows]);

    useEffect(() => {
        setPageContribution({
            commands,
            dynamicCommands,
            rowAdapter: rowAdapter as RowAdapter<unknown>,
            placeholder: 'Search routes or run an action…',
        });
        return () => setPageContribution(null);
    }, [commands, dynamicCommands, rowAdapter, setPageContribution]);

    const pageHeader = (
        <CommandPaletteHeader
            title="Route FIB"
            placeholder="Search routes or run an action…"
            actions={<>
                <FIBYamlIO
                    key={currentConfig || '__none'}
                    configName={currentConfig}
                    rows={rawRows}
                    onImport={handlers.handleImportYaml}
                    disabled={!currentConfig}
                />
                <Button view="action" onClick={() => openAdd()}>
                    <Icon data={Plus} size={16} />
                    Add Route
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
                        message="No FIB configurations found."
                        actionLabel="Add Config"
                        onAction={() => setAddConfigOpen(true)}
                    />
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            counts={routeCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={handleConfigSelect}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />
                        <div className="yn-toolbar-bordered">
                            <div style={{ flex: 1 }} />
                            <div style={{ flexBasis: 230, flexShrink: 1 }}>
                                <SearchInput
                                    value={search}
                                    onUpdate={handleSearchChange}
                                    placeholder="Filter routes…"
                                    enableFocusShortcut={false}
                                    showShortcutHint={false}
                                    icon={Funnel}
                                />
                            </div>
                            <RowCountDisplay filtered={visibleRows.length} total={rawRows.length} />
                        </div>
                        <div className="yn-content">
                            <FIBTable
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
                    <BulkBar count={selectedIds.size} itemNoun="route" onDelete={() => setDeleteConfirmOpen(true)} onClear={() => setSelectedIds(new Set())} />
                )}

                <BulkDeleteModal open={deleteConfirmOpen} count={selectedIds.size} itemNoun="route" configName={currentConfig} onClose={() => setDeleteConfirmOpen(false)} onConfirm={handlers.handleBulkDelete} />

                <FIBDrawer ref={drawerRef} open={!!editingRow} row={editingRow} index={editingIndex} total={rawRows.length} onClose={handlers.closeDrawer} onChange={handlers.handleRowChange} onDelete={handlers.handleDeleteRow} onJump={handlers.handleJumpEdit} />

                {diffModalOpen && (
                    <FIBSaveDiffModal configName={currentConfig} draftRows={rawRows} serverRows={rawServerRows} onClose={() => setDiffModalOpen(false)} onApply={handlers.handleCommit} />
                )}

                <AddConfigModal
                    open={addConfigOpen}
                    onClose={() => setAddConfigOpen(false)}
                    onCreate={(name) => { dispatchDraft({ type: 'ADD_CONFIG', configName: name }); setActiveConfig(name); setAddConfigOpen(false); }}
                    title="Add FIB config"
                    placeholder="e.g. route0"
                    existingNames={draftConfigs}
                />

                <DeleteConfigModal open={deleteConfigOpen} configName={currentConfig} onClose={() => setDeleteConfigOpen(false)} onConfirm={handlers.handleDeleteConfig} />

            </div>
        </PageLayout>
    );
};

export default RoutePage;
