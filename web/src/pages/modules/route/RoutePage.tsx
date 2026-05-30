import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '@gravity-ui/uikit';
import { useSearchParams } from 'react-router-dom';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { useFIBDraft } from './useFIBDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { FIBRowItem } from './types';
import { FIBTable } from './FIBTable';
import FIBDrawer from './FIBDrawer';
import type { FIBDrawerHandle } from './FIBDrawer';
import FIBYamlIO from './FIBYamlIO';
import { FIBSaveDiffModal } from './FIBSaveDiffModal';
import {
    AddConfigModal, DeleteConfigModal, BulkDeleteModal,
    DraftPageToolbar, useDraftShortcuts, useDraftDragDrop, useDraftPageHandlers,
} from '../../_shared/draft';
import '../../../styles/draft-page.scss';

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

    const setActiveConfig = useCallback((configName: string): void => {
        updateParams({ [QP_CONFIG]: configName || null });
    }, [updateParams]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig))) ? queryConfig : (draftConfigs[0] || '');

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

    const rawRows: FIBRowItem[] = draftRows(currentConfig);
    const rawServerRows: FIBRowItem[] = serverRows(currentConfig);
    const currentIsDirty = isDirty(currentConfig);

    const routeCounts = useMemo((): Map<string, number> => {
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

    const statusById = useMemo((): Map<string, import('./types').FIBRowStatus> => {
        const m = new Map<string, import('./types').FIBRowStatus>();
        const serverById = new Map(rawServerRows.map((r) => [r.id, r]));
        for (const r of rawRows) {
            const s = serverById.get(r.id);
            if (!s) m.set(r.id, 'added');
            else m.set(r.id, (s.prefix === r.prefix && s.dst_mac === r.dst_mac && s.src_mac === r.src_mac && s.device === r.device) ? 'same' : 'changed');
        }
        return m;
    }, [rawRows, rawServerRows]);

    const removedRows = useMemo((): FIBRowItem[] => {
        const localIds = new Set(rawRows.map((r) => r.id));
        return rawServerRows.filter((r) => !localIds.has(r.id));
    }, [rawRows, rawServerRows]);

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

    const openAdd = () => {
        const newRow: FIBRowItem = { id: makeRowId(), prefix: '', dst_mac: '', src_mac: '', device: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    };

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    const handleConfigSelect = useCallback((cfg: string): void => {
        setActiveConfig(cfg);
    }, [setActiveConfig]);

    useDraftShortcuts({
        rows: rawRows, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        onDeleteRow: handlers.handleDeleteRow,
    });

    const pageHeader = (
            <DraftPageToolbar
                title="Route FIB"
                searchValue={search}
                onSearchChange={handleSearchChange}
                searchPlaceholder="Search prefix, MAC or device…"
                yamlSlot={currentConfig ? (
                    <FIBYamlIO key={currentConfig} configName={currentConfig} rows={rawRows} onImport={handlers.handleImportYaml} />
                ) : undefined}
            addLabel="Add Route"
            onAdd={openAdd}
        />
    );

    if (loading) return <PageLayout header={pageHeader}><PageLoader loading size="l" /></PageLayout>;

    return (
        <PageLayout header={pageHeader}>
            <div className="fw-page">
                {draftConfigs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No FIB configurations found.</div>
                        <Button view="action" onClick={() => setAddConfigOpen(true)}>Add Config</Button>
                    </div>
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
                        <div className="fw-content">
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
