import React, { useMemo, useRef, useState } from 'react';
import { Button } from '@gravity-ui/uikit';
import { PageLayout, PageLoader, ConfigTabStrip, BulkBar } from '../../../components';
import { usePrefixDraft } from './usePrefixDraft';
import { useUnsavedChangesBlocker } from '../../builtin/_shared/lane-editor';
import type { PrefixRowItem } from './types';
import { PrefixTable } from './PrefixTable';
import PrefixDrawer from './PrefixDrawer';
import type { PrefixDrawerHandle } from './PrefixDrawer';
import PrefixYamlIO from './PrefixYamlIO';
import { PrefixSaveDiffModal } from './PrefixSaveDiffModal';
import {
    AddConfigModal, DeleteConfigModal, BulkDeleteModal,
    DraftPageToolbar, useDraftShortcuts, useDraftDragDrop, useDraftPageHandlers,
} from '../../_shared/draft';
import '../../../styles/draft-page.scss';

let idCounter = 0;
const makeRowId = (): string => `new-${++idCounter}-${Date.now()}`;

const DecapPage: React.FC = () => {
    const {
        draftConfigs, loading, draftRows, serverRows, isDirty, anyDirty,
        dispatchDraft, commitConfig, discardConfig,
    } = usePrefixDraft();

    const [activeConfig, setActiveConfig] = useState<string>('');
    const [search, setSearch] = useState('');
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);

    const drawerRef = useRef<PrefixDrawerHandle>(null);
    const dragDrop = useDraftDragDrop();

    useUnsavedChangesBlocker(anyDirty);

    const currentConfig = activeConfig || draftConfigs[0] || '';
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

    const openAdd = () => {
        const newRow: PrefixRowItem = { id: makeRowId(), prefix: '' };
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: newRow });
        setActiveRowId(newRow.id);
        setEditingRowId(newRow.id);
    };

    useDraftShortcuts({
        rows: rawRows, activeRowId, setActiveRowId, editingRowId, setEditingRowId,
        onDeleteRow: handlers.handleDeleteRow,
    });

    const pageHeader = (
            <DraftPageToolbar
                title="Decap"
                searchValue={search}
                onSearchChange={setSearch}
                searchPlaceholder="Search prefix…"
                yamlSlot={currentConfig ? (
                <PrefixYamlIO configName={currentConfig} rows={rawRows} onImport={handlers.handleImportYaml} />
            ) : undefined}
            addLabel="Add Prefix"
            onAdd={openAdd}
        />
    );

    if (loading) return <PageLayout header={pageHeader}><PageLoader loading size="l" /></PageLayout>;

    return (
        <PageLayout header={pageHeader}>
            <div className="fw-page">
                {draftConfigs.length === 0 ? (
                    <div className="fw-empty-page">
                        <div className="fw-empty-page__message">No decap configurations found.</div>
                        <Button view="action" onClick={() => setAddConfigOpen(true)}>Add Config</Button>
                    </div>
                ) : (
                    <>
                        <ConfigTabStrip
                            configs={draftConfigs}
                            activeConfig={currentConfig}
                            counts={prefixCounts}
                            dirtyConfigs={dirtySet}
                            onSelect={(c) => { setActiveConfig(c); setActiveRowId(null); setEditingRowId(null); setSelectedIds(new Set()); }}
                            onAddConfig={() => setAddConfigOpen(true)}
                        />
                        <div className="fw-content">
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
