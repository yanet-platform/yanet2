import { useEffect, useMemo } from 'react';
import { useConfigQuerySync, useDirtyConfigSet } from '../../hooks';
import { computeRowStatuses } from './rowStatus';
import type { RowStatusResult } from './rowStatus';
import type { UseDraftPageStateResult } from './useDraftPageState';
import type { RowStatus } from '../VirtualTable';

/** Options for the useDraftPageDerived hook. */
export interface UseDraftPageDerivedOptions<T extends { id: string }> {
    pageState: UseDraftPageStateResult;
    draftRows: (config: string) => T[];
    serverRows: (config: string) => T[];
    isDirty: (config: string) => boolean;
    draftConfigs: string[];
    loading: boolean;
    configParamKey: string;
    matchesSearch: (row: T, query: string) => boolean;
    rowsEqual: (server: T, local: T) => boolean;
}

/** Result returned by useDraftPageDerived. */
export interface UseDraftPageDerivedResult<T extends { id: string }> {
    rawRows: T[];
    rawServerRows: T[];
    currentIsDirty: boolean;
    rowCounts: Map<string, number>;
    dirtySet: Set<string>;
    visibleRows: T[];
    statusById: Map<string, RowStatus>;
    removedRows: T[];
}

/**
 * Derives row collections, row counts, dirty sets, and row statuses for a
 * draft-based config page from the active config state.
 */
export const useDraftPageDerived = <T extends { id: string }>({
    pageState,
    draftRows,
    serverRows,
    isDirty,
    draftConfigs,
    loading,
    configParamKey,
    matchesSearch,
    rowsEqual,
}: UseDraftPageDerivedOptions<T>): UseDraftPageDerivedResult<T> => {
    const { currentConfig, queryConfig, search, searchParams, updateParams, dragDrop } = pageState;
    const { handleDragLeave } = dragDrop;
    const {
        setActiveRowId,
        setEditingRowId,
        setSelectedIds,
        setDeleteConfirmOpen,
        setDeleteConfigOpen,
        setDiffModalOpen,
    } = pageState;

    useConfigQuerySync({ currentConfig, loading, queryConfig, paramKey: configParamKey, searchParams, updateParams });

    useEffect(() => {
        setActiveRowId(null);
        setEditingRowId(null);
        setSelectedIds(new Set());
        setDeleteConfirmOpen(false);
        setDeleteConfigOpen(false);
        setDiffModalOpen(false);
        handleDragLeave();
    }, [currentConfig, handleDragLeave, setActiveRowId, setEditingRowId, setSelectedIds, setDeleteConfirmOpen, setDeleteConfigOpen, setDiffModalOpen]);

    const rawRows: T[] = draftRows(currentConfig);
    const rawServerRows: T[] = serverRows(currentConfig);
    const currentIsDirty = isDirty(currentConfig);

    const rowCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach((c) => m.set(c, draftRows(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRows]);

    const dirtySet = useDirtyConfigSet(draftConfigs, isDirty);

    const visibleRows = useMemo((): T[] => {
        const q = search.trim().toLowerCase();
        if (!q) return rawRows;
        return rawRows.filter((r) => matchesSearch(r, q));
    }, [rawRows, search, matchesSearch]);

    const { statusById, removedRows }: RowStatusResult<T> = useMemo(
        () => computeRowStatuses(rawRows, rawServerRows, rowsEqual),
        [rawRows, rawServerRows, rowsEqual],
    );

    return {
        rawRows,
        rawServerRows,
        currentIsDirty,
        rowCounts,
        dirtySet,
        visibleRows,
        statusById,
        removedRows,
    };
};
