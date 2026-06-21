import { useCallback, useMemo, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers, useUnsavedChangesBlocker } from '../../hooks';
import { useDraftDragDrop } from './useDraftDragDrop';
import type { UseDraftDragDropResult } from './useDraftDragDrop';

/** Options for the useDraftPageState hook. */
export interface UseDraftPageStateOptions {
    loading: boolean;
    draftConfigs: string[];
    anyDirty: boolean;
    configParamKey: string;
    searchParamKey: string;
}

/** Result returned by useDraftPageState. */
export interface UseDraftPageStateResult {
    searchParams: URLSearchParams;
    queryConfig: string | null;
    search: string;
    activeRowId: string | null;
    setActiveRowId: React.Dispatch<React.SetStateAction<string | null>>;
    editingRowId: string | null;
    setEditingRowId: React.Dispatch<React.SetStateAction<string | null>>;
    selectedIds: Set<string>;
    setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>;
    deleteConfirmOpen: boolean;
    setDeleteConfirmOpen: React.Dispatch<React.SetStateAction<boolean>>;
    diffModalOpen: boolean;
    setDiffModalOpen: React.Dispatch<React.SetStateAction<boolean>>;
    addConfigOpen: boolean;
    setAddConfigOpen: React.Dispatch<React.SetStateAction<boolean>>;
    deleteConfigOpen: boolean;
    setDeleteConfigOpen: React.Dispatch<React.SetStateAction<boolean>>;
    dragDrop: UseDraftDragDropResult;
    updateParams: (updates: Record<string, string | null>) => void;
    setActiveConfig: (configName: string) => void;
    currentConfig: string;
}

/**
 * Manages URL search-param state, modal toggles, drag-drop, and the active-config
 * derivation common to draft-based config pages.
 */
export const useDraftPageState = ({
    loading,
    draftConfigs,
    anyDirty,
    configParamKey,
    searchParamKey,
}: UseDraftPageStateOptions): UseDraftPageStateResult => {
    const [searchParams, setSearchParams] = useSearchParams();

    const queryConfig = useMemo(() => searchParams.get(configParamKey), [searchParams, configParamKey]);
    const search = useMemo(() => searchParams.get(searchParamKey) || '', [searchParams, searchParamKey]);

    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [editingRowId, setEditingRowId] = useState<string | null>(null);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);

    const dragDrop = useDraftDragDrop();

    useUnsavedChangesBlocker(anyDirty);

    const { updateParams } = useSearchParamHelpers(setSearchParams);

    const setActiveConfig = useCallback((configName: string): void => {
        updateParams({ [configParamKey]: configName || null });
    }, [updateParams, configParamKey]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig))) ? queryConfig : (draftConfigs[0] || '');

    return {
        searchParams,
        queryConfig,
        search,
        activeRowId,
        setActiveRowId,
        editingRowId,
        setEditingRowId,
        selectedIds,
        setSelectedIds,
        deleteConfirmOpen,
        setDeleteConfirmOpen,
        diffModalOpen,
        setDiffModalOpen,
        addConfigOpen,
        setAddConfigOpen,
        deleteConfigOpen,
        setDeleteConfigOpen,
        dragDrop,
        updateParams,
        setActiveConfig,
        currentConfig,
    };
};
