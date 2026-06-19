import React, { useCallback, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useSearchParamHelpers, usePageKeyboardShortcuts, useDirtyConfigSet, useConfigQuerySync, useTabCycle, useUnsavedChangesBlocker } from '../../hooks';
import { DRAWER_TRANSITION_MS } from './constants';

/** Minimal drawer handle interface required by the save-press handler. */
interface DrawerHandle {
    flushAndApply(): void;
}

/** Dispatch actions emitted by useRulePageState for draft mutation. */
type RuleDispatchAction<TRule> =
    | { type: 'ADD_RULE'; configName: string; rule: TRule }
    | { type: 'UPDATE_RULE_AT_INDEX'; configName: string; index: number; rule: TRule }
    | { type: 'REMOVE_RULES'; configName: string; indices: number[] };

/** Options for useRulePageState. */
export interface UseRulePageStateOptions<TRule, TItem extends { id: string; index: number }, TDraft> {
    draftConfigs: string[];
    loading: boolean;
    anyDirty: boolean;
    isDirty: (config: string) => boolean;
    draftRules: (config: string) => TRule[];
    dispatchDraft: (action: RuleDispatchAction<TRule>) => void;
    saveConfig: (config: string) => Promise<void>;
    discardConfig: (config: string) => void;
    toRule: (draft: TDraft) => TRule;
    /**
     * Rebuilds a draft from an existing item.
     *
     * Used to canonicalize the rule being edited so an apply that did not
     * change anything is detected and skipped (avoids a spurious dirty flag).
     */
    itemToDraft: (item: TItem) => TDraft;
    cloneItem: (item: TItem) => TItem;
    requireConfigForAdd: boolean;
    clearSelectionOnTabSelect: boolean;
}

/** Result returned by useRulePageState. */
export interface UseRulePageStateResult<TItem extends { id: string; index: number }, TDraft, THandle extends DrawerHandle = DrawerHandle> {
    currentConfig: string;
    search: string;
    updateParams: (updates: Record<string, string | null>) => void;
    clearConfigParamIfCurrent: (name: string) => void;
    selectedIds: Set<string>;
    setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>;
    activeRowId: string | null;
    setActiveRowId: React.Dispatch<React.SetStateAction<string | null>>;
    drawer: { open: boolean; mode: 'add' | 'edit'; item: TItem | null };
    setDrawer: React.Dispatch<React.SetStateAction<{ open: boolean; mode: 'add' | 'edit'; item: TItem | null }>>;
    deleteConfirmOpen: boolean;
    setDeleteConfirmOpen: React.Dispatch<React.SetStateAction<boolean>>;
    addConfigOpen: boolean;
    setAddConfigOpen: React.Dispatch<React.SetStateAction<boolean>>;
    deleteConfigOpen: boolean;
    setDeleteConfigOpen: React.Dispatch<React.SetStateAction<boolean>>;
    diffModalOpen: boolean;
    setDiffModalOpen: React.Dispatch<React.SetStateAction<boolean>>;
    flashRowId: string | null;
    setFlashRowId: React.Dispatch<React.SetStateAction<string | null>>;
    setDeleteInFlightConfig: React.Dispatch<React.SetStateAction<string | null>>;
    drawerRef: React.RefObject<THandle | null>;
    ruleCounts: Map<string, number>;
    dirtySet: Set<string>;
    currentIsDirty: boolean;
    openAdd: () => void;
    openEdit: (item: TItem) => void;
    closeDrawer: () => void;
    handleDrawerApply: (draft: TDraft) => void;
    handleDeleteItem: (item: TItem) => void;
    handleDuplicate: (item: TItem) => void;
    handleSave: () => Promise<void>;
    handleSavePress: () => void;
    handleDiscard: () => void;
    handleSearchChange: (value: string) => void;
    handleJumpToRow: (id: string) => void;
    handleTabSelect: (cfg: string) => void;
}

/** Manages the shared URL-param, modal, drawer, and handler plumbing common to AclPage and ForwardPage. */
export const useRulePageState = <TRule, TItem extends { id: string; index: number }, TDraft, THandle extends DrawerHandle = DrawerHandle>({
    draftConfigs,
    loading,
    anyDirty,
    isDirty,
    draftRules,
    dispatchDraft,
    saveConfig,
    discardConfig,
    toRule,
    itemToDraft,
    cloneItem,
    requireConfigForAdd,
    clearSelectionOnTabSelect,
}: UseRulePageStateOptions<TRule, TItem, TDraft>): UseRulePageStateResult<TItem, TDraft, THandle> => {
    const QP_CONFIG = 'config';
    const QP_SEARCH = 'search';

    const [searchParams, setSearchParams] = useSearchParams();

    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [activeRowId, setActiveRowId] = useState<string | null>(null);
    const [drawer, setDrawer] = useState<{ open: boolean; mode: 'add' | 'edit'; item: TItem | null }>({
        open: false,
        mode: 'add',
        item: null,
    });
    const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
    const [addConfigOpen, setAddConfigOpen] = useState(false);
    const [deleteConfigOpen, setDeleteConfigOpen] = useState(false);
    const [diffModalOpen, setDiffModalOpen] = useState(false);
    const [flashRowId, setFlashRowId] = useState<string | null>(null);
    const [deleteInFlightConfig, setDeleteInFlightConfig] = useState<string | null>(null);
    const drawerRef = useRef<THandle>(null);

    const queryConfig = useMemo(() => searchParams.get(QP_CONFIG), [searchParams]);
    const search = useMemo(() => searchParams.get(QP_SEARCH) || '', [searchParams]);

    const currentConfig = (queryConfig && (loading || draftConfigs.includes(queryConfig) || queryConfig === deleteInFlightConfig))
        ? queryConfig
        : (draftConfigs[0] || '');

    const { updateParams, clearConfigParamIfCurrent } = useSearchParamHelpers(setSearchParams, QP_CONFIG);

    useConfigQuerySync({ currentConfig, loading, queryConfig, paramKey: QP_CONFIG, searchParams, updateParams });

    useUnsavedChangesBlocker(anyDirty);

    const ruleCounts = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        draftConfigs.forEach(c => m.set(c, draftRules(c).length));
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [draftConfigs, draftRules]);

    const dirtySet = useDirtyConfigSet(draftConfigs, isDirty);

    const currentIsDirty = isDirty(currentConfig);

    const openAdd = useCallback((): void => {
        if (requireConfigForAdd && !currentConfig) {
            return;
        }
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: null });
    }, [requireConfigForAdd, currentConfig]);

    const openEdit = useCallback((item: TItem): void => {
        setActiveRowId(item.id);
        setDrawer({ open: true, mode: 'edit', item });
    }, []);

    const closeDrawer = useCallback((): void => {
        // Keep activeRowId so the highlighted row stays selected after the
        // editor closes — the user can keep arrow-navigating from there.
        setDrawer(d => ({ ...d, open: false }));
        setTimeout(() => {
            setDrawer(d => ({ ...d, item: null }));
        }, DRAWER_TRANSITION_MS);
    }, []);

    const handleDrawerApply = useCallback((draft: TDraft): void => {
        const rule = toRule(draft);
        if (drawer.mode === 'add') {
            dispatchDraft({ type: 'ADD_RULE', configName: currentConfig, rule });
        } else if (drawer.item) {
            // Skip the update when nothing changed. Both sides are compared in
            // canonical (toRule) form so the round-trip's array/key normalization
            // does not look like an edit and flip the config dirty on a no-op
            // apply (e.g. opening a rule and pressing Ctrl/Cmd+Enter).
            const original = toRule(itemToDraft(drawer.item));
            if (JSON.stringify(rule) !== JSON.stringify(original)) {
                dispatchDraft({ type: 'UPDATE_RULE_AT_INDEX', configName: currentConfig, index: drawer.item.index, rule });
            }
        }
        closeDrawer();
    }, [drawer, currentConfig, dispatchDraft, toRule, itemToDraft, closeDrawer]);

    const handleDeleteItem = useCallback((item: TItem): void => {
        dispatchDraft({ type: 'REMOVE_RULES', configName: currentConfig, indices: [item.index] });
        closeDrawer();
    }, [currentConfig, dispatchDraft, closeDrawer]);

    const handleDuplicate = useCallback((item: TItem): void => {
        setActiveRowId(null);
        setDrawer({ open: true, mode: 'add', item: cloneItem(item) });
    }, [cloneItem]);

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

    const handleSearchChange = useCallback((value: string): void => {
        updateParams({ [QP_SEARCH]: value || null });
    }, [updateParams]);

    const handleJumpToRow = useCallback((id: string): void => {
        setFlashRowId(null);
        setTimeout(() => setFlashRowId(id), 0);
    }, []);

    const handleTabSelect = useCallback((cfg: string): void => {
        updateParams({ [QP_CONFIG]: cfg || null });
        if (clearSelectionOnTabSelect) {
            setSelectedIds(new Set());
            setActiveRowId(null);
        }
    }, [updateParams, clearSelectionOnTabSelect]);

    useTabCycle({
        tabs: draftConfigs,
        activeTab: currentConfig,
        onSelect: handleTabSelect,
        enabled: !loading,
    });

    usePageKeyboardShortcuts({
        onNewRule: openAdd,
    });

    return {
        currentConfig,
        search,
        updateParams,
        clearConfigParamIfCurrent,
        selectedIds,
        setSelectedIds,
        activeRowId,
        setActiveRowId,
        drawer,
        setDrawer,
        deleteConfirmOpen,
        setDeleteConfirmOpen,
        addConfigOpen,
        setAddConfigOpen,
        deleteConfigOpen,
        setDeleteConfigOpen,
        diffModalOpen,
        setDiffModalOpen,
        flashRowId,
        setFlashRowId,
        setDeleteInFlightConfig,
        drawerRef,
        ruleCounts,
        dirtySet,
        currentIsDirty,
        openAdd,
        openEdit,
        closeDrawer,
        handleDrawerApply,
        handleDeleteItem,
        handleDuplicate,
        handleSave,
        handleSavePress,
        handleDiscard,
        handleSearchChange,
        handleJumpToRow,
        handleTabSelect,
    };
};
