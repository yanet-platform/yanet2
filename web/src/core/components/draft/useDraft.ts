import { useCallback, useEffect, useMemo, useReducer, useState } from 'react';
import { useConfigListCache } from '../../hooks/useConfigListCache';
import { toaster } from '../../utils';
import type { DraftState, DraftAction } from './draftReducer';

type Reducer<T> = (state: DraftState<T>, action: DraftAction<T>) => DraftState<T>;

interface UseDraftOpts<T> {
    /** Fetch all configs from the server and return them as name+rows pairs. */
    load: () => Promise<Array<{ name: string; rows: T[] }>>;
    /** Persist a single config to the server. */
    commit: (configName: string, draftRows: T[], serverRows: T[]) => Promise<void>;
    reducer: Reducer<T>;
    initialState: DraftState<T>;
    /** Toast subject used in success/failure messages (e.g. 'fib' or 'prefix'). */
    toastSubject: string;
    /** Human-readable subject used in error messages (e.g. 'FIB' or 'decap prefix'). */
    errorSubject: string;
    /** Gateway-scoped cache key for the config tab strip (e.g. 'decap' or 'route'). */
    cacheKey: string;
}

export interface UseDraftResult<T> {
    draftConfigs: string[];
    loading: boolean;
    /** True when the initial load failed and no configs were seeded; cleared on a successful reload. */
    loadFailed: boolean;
    draftRows: (configName: string) => T[];
    serverRows: (configName: string) => T[];
    isDirty: (configName: string) => boolean;
    anyDirty: boolean;
    dispatchDraft: (action: DraftAction<T>) => void;
    commitConfig: (configName: string) => Promise<void>;
    discardConfig: (configName: string) => void;
}

const EMPTY_ROWS: never[] = [];

/**
 * Generic draft-layer orchestrator for a multi-config table page.
 *
 * Fetches server state on mount, exposes a local-draft dispatch layer,
 * and commits individual configs back to the server via the provided callbacks.
 */
export function useDraft<T extends { id?: unknown }>({
    load,
    commit,
    reducer,
    initialState,
    toastSubject,
    errorSubject,
    cacheKey,
}: UseDraftOpts<T>): UseDraftResult<T> {
    const [state, rawDispatch] = useReducer(reducer, initialState);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const { write: writeCache } = useConfigListCache(cacheKey);

    const dispatchDraft = useCallback((action: DraftAction<T>): void => {
        rawDispatch(action);
    }, []);

    const doLoad = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const configs = await load();
            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
            writeCache({
                configs: configs.map((cfg) => cfg.name),
                counts: Object.fromEntries(configs.map((cfg) => [cfg.name, cfg.rows.length])),
            });
            setLoadFailed(false);
        } catch (err) {
            toaster.error(`${toastSubject}-draft-load`, `Failed to load ${errorSubject} configurations`, err);
            setLoadFailed(true);
        } finally {
            setLoading(false);
        }
    }, [load, toastSubject, errorSubject, writeCache]);

    useEffect(() => {
        doLoad();
    }, [doLoad]);

    const commitConfig = useCallback(async (configName: string): Promise<void> => {
        const draft = state.draft[configName] ?? (EMPTY_ROWS as unknown as T[]);
        const server = state.server[configName] ?? (EMPTY_ROWS as unknown as T[]);
        try {
            await commit(configName, draft, server);
            rawDispatch({ type: 'MARK_COMMITTED', configName });
            toaster.success(`${toastSubject}-commit-${configName}`, `${errorSubject} "${configName}" committed.`);
        } catch (err) {
            toaster.error(
                `${toastSubject}-commit-err-${configName}`,
                `Failed to commit "${configName}"`,
                err,
            );
            throw err;
        }
    }, [state.draft, state.server, commit, toastSubject, errorSubject]);

    const discardConfig = useCallback((configName: string): void => {
        rawDispatch({ type: 'DISCARD_CONFIG', configName });
    }, []);

    const draftRowsFor = useCallback(
        (configName: string): T[] => state.draft[configName] ?? (EMPTY_ROWS as unknown as T[]),
        [state.draft],
    );

    const serverRowsFor = useCallback(
        (configName: string): T[] => state.server[configName] ?? (EMPTY_ROWS as unknown as T[]),
        [state.server],
    );

    const isDirty = useCallback(
        (configName: string): boolean => state.dirty.has(configName),
        [state.dirty],
    );

    const draftConfigs = useMemo(
        () => [...state.serverConfigs, ...state.localOnlyConfigs],
        [state.serverConfigs, state.localOnlyConfigs],
    );

    const anyDirty = state.dirty.size > 0;

    return {
        draftConfigs,
        loading,
        loadFailed,
        draftRows: draftRowsFor,
        serverRows: serverRowsFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        commitConfig,
        discardConfig,
    };
}
