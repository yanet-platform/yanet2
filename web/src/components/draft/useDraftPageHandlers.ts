import { useCallback } from 'react';
import type { DraftAction } from './draftReducer';
import type { UseDraftDragDropResult } from './useDraftDragDrop';

interface UseDraftPageHandlersOpts<T extends { id: string }> {
    currentConfig: string;
    rawRows: T[];
    editingIndex: number;
    activeRowId: string | null;
    editingRowId: string | null;
    selectedIds: Set<string>;
    dispatchDraft: (action: DraftAction<T>) => void;
    commitConfig: (name: string) => Promise<void>;
    discardConfig: (name: string) => void;
    drawerFlush: (() => void) | null;
    setActiveConfig: (v: string) => void;
    setActiveRowId: (v: string | null) => void;
    setEditingRowId: (v: string | null) => void;
    setSelectedIds: (v: Set<string>) => void;
    setDiffModalOpen: (v: boolean) => void;
    setDeleteConfirmOpen: (v: boolean) => void;
    setDeleteConfigOpen: (v: boolean) => void;
    dragDrop: UseDraftDragDropResult;
}

export interface UseDraftPageHandlersResult<T extends { id: string }> {
    closeDrawer: () => void;
    handleRowChange: (updated: T) => void;
    handleDeleteRow: (row: T) => void;
    handleBulkDelete: () => void;
    handleRestoreRow: (row: T) => void;
    handleJumpEdit: (delta: number) => void;
    handleImportYaml: (rows: T[], mode: 'replace' | 'append') => void;
    handleCommitPress: () => void;
    handleCommit: () => Promise<void>;
    handleDiscard: () => void;
    handleDeleteConfig: () => void;
    handleDrop: (id: string, e: React.DragEvent) => void;
}

/**
 * Returns the set of draft-page action callbacks that are structurally
 * identical across all draft-table pages (Route, Decap, Forward).
 *
 * Callers supply page-specific state setters and the draft dispatch function.
 */
export function useDraftPageHandlers<T extends { id: string }>({
    currentConfig,
    rawRows,
    editingIndex,
    activeRowId,
    editingRowId,
    selectedIds,
    dispatchDraft,
    commitConfig,
    discardConfig,
    drawerFlush,
    setActiveConfig,
    setActiveRowId,
    setEditingRowId,
    setSelectedIds,
    setDiffModalOpen,
    setDeleteConfirmOpen,
    setDeleteConfigOpen,
    dragDrop,
}: UseDraftPageHandlersOpts<T>): UseDraftPageHandlersResult<T> {
    const closeDrawer = useCallback((): void => {
        setEditingRowId(null);
    }, [setEditingRowId]);

    const handleRowChange = useCallback((updated: T): void => {
        dispatchDraft({ type: 'UPDATE_ROW', configName: currentConfig, id: updated.id, patch: updated });
    }, [currentConfig, dispatchDraft]);

    const handleDeleteRow = useCallback((row: T): void => {
        dispatchDraft({ type: 'REMOVE_ROW', configName: currentConfig, id: row.id });
        if (editingRowId === row.id) setEditingRowId(null);
        if (activeRowId === row.id) setActiveRowId(null);
    }, [currentConfig, dispatchDraft, editingRowId, activeRowId, setEditingRowId, setActiveRowId]);

    const handleBulkDelete = useCallback((): void => {
        dispatchDraft({ type: 'REMOVE_ROWS', configName: currentConfig, ids: Array.from(selectedIds) });
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
    }, [selectedIds, currentConfig, dispatchDraft, setSelectedIds, setDeleteConfirmOpen]);

    const handleRestoreRow = useCallback((row: T): void => {
        dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: { ...row } });
    }, [currentConfig, dispatchDraft]);

    const handleJumpEdit = useCallback((delta: number): void => {
        const next = editingIndex + delta;
        if (next < 0 || next >= rawRows.length) return;
        setEditingRowId(rawRows[next].id);
        setActiveRowId(rawRows[next].id);
    }, [editingIndex, rawRows, setEditingRowId, setActiveRowId]);

    const handleImportYaml = useCallback((rows: T[], mode: 'replace' | 'append'): void => {
        if (mode === 'replace') {
            dispatchDraft({ type: 'REPLACE_ALL_ROWS', configName: currentConfig, rows });
        } else {
            for (const r of rows) {
                dispatchDraft({ type: 'ADD_ROW', configName: currentConfig, row: r });
            }
        }
    }, [currentConfig, dispatchDraft]);

    const handleCommitPress = useCallback((): void => {
        drawerFlush?.();
        setDiffModalOpen(true);
    }, [drawerFlush, setDiffModalOpen]);

    const handleCommit = useCallback(async (): Promise<void> => {
        await commitConfig(currentConfig);
        setDiffModalOpen(false);
    }, [currentConfig, commitConfig, setDiffModalOpen]);

    const handleDiscard = useCallback((): void => {
        discardConfig(currentConfig);
    }, [currentConfig, discardConfig]);

    const handleDeleteConfig = useCallback((): void => {
        dispatchDraft({ type: 'DELETE_CONFIG', configName: currentConfig });
        setActiveConfig('');
        setDeleteConfigOpen(false);
    }, [currentConfig, dispatchDraft, setActiveConfig, setDeleteConfigOpen]);

    const handleDrop = useCallback((id: string, e: React.DragEvent): void => {
        dragDrop.handleDrop(id, e, rawRows, (reordered) => {
            dispatchDraft({ type: 'REORDER_ROWS', configName: currentConfig, rows: reordered });
        });
    }, [dragDrop, rawRows, currentConfig, dispatchDraft]);

    return {
        closeDrawer,
        handleRowChange,
        handleDeleteRow,
        handleBulkDelete,
        handleRestoreRow,
        handleJumpEdit,
        handleImportYaml,
        handleCommitPress,
        handleCommit,
        handleDiscard,
        handleDeleteConfig,
        handleDrop,
    };
}
