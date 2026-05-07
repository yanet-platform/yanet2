import { useCallback, useEffect, useReducer, useState } from 'react';
import { API } from '../../../../api';
import { toaster } from '../../../../utils';
import type { NetworkFunction, FunctionsAction } from '../types';
import { functionsReducer, initialState, discardEdits } from '../reducer';
import { apiToLocal, localToApi } from '../wire';

export interface UseFunctionsDataResult {
    functions: NetworkFunction[];
    loading: boolean;
    isDirty: (fnId: string) => boolean;
    getServerFn: (fnId: string) => NetworkFunction | null;
    dispatch: (action: FunctionsAction) => void;
    saveFn: (fnId: string) => Promise<void>;
    discardFn: (fnId: string) => void;
    createFn: (name: string) => Promise<boolean>;
}

/**
 * Loads all network functions from the API, manages local edit state via useReducer,
 * and exposes per-function save/discard/create operations.
 */
export const useFunctionsData = (): UseFunctionsDataResult => {
    const [state, rawDispatch] = useReducer(functionsReducer, initialState);
    const [loading, setLoading] = useState(true);
    const [fnIds, setFnIds] = useState<string[]>([]);

    const dispatch = useCallback((action: FunctionsAction) => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const listResp = await API.functions.list({});
            const ids = listResp.ids ?? [];
            const names = ids.map(id => id.name ?? '').filter(Boolean);
            setFnIds(names);

            await Promise.all(ids.map(async (fid) => {
                try {
                    const resp = await API.functions.get({ id: fid });
                    if (resp.function) {
                        rawDispatch({ type: 'LOAD_FUNCTION', fn: apiToLocal(resp.function) });
                    }
                } catch (err) {
                    toaster.error('fn-ng-load', `Failed to load function ${fid.name}`, err);
                }
            }));
        } catch (err) {
            toaster.error('fn-ng-list', 'Failed to load functions', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const saveFn = useCallback(async (fnId: string): Promise<void> => {
        const fn = state.local[fnId];
        if (!fn) {
            return;
        }
        try {
            await API.functions.update({ function: localToApi(fn) });
            // Sync server snapshot by re-dispatching as LOAD_FUNCTION to clear dirty.
            rawDispatch({ type: 'LOAD_FUNCTION', fn });
            toaster.success(`fn-ng-save-${fnId}`, `Function "${fnId}" saved.`);
        } catch (err) {
            toaster.error(`fn-ng-save-err-${fnId}`, `Failed to save "${fnId}"`, err);
        }
    }, [state.local]);

    const discardFn = useCallback((fnId: string): void => {
        const next = discardEdits(state, fnId);
        const reverted = next.local[fnId];
        if (reverted) {
            rawDispatch({ type: 'LOAD_FUNCTION', fn: reverted });
        }
    }, [state]);

    const isDirty = useCallback((fnId: string): boolean => state.dirty.has(fnId), [state.dirty]);

    const getServerFn = useCallback((fnId: string): NetworkFunction | null =>
        state.server[fnId] ?? null, [state.server]);

    const createFn = useCallback(async (name: string): Promise<boolean> => {
        try {
            await API.functions.update({ function: { id: { name }, chains: [] } });
            await load();
            toaster.success(`fn-ng-create-${name}`, `Function "${name}" created.`);
            return true;
        } catch (err) {
            toaster.error(`fn-ng-create-err-${name}`, `Failed to create "${name}"`, err);
            return false;
        }
    }, [load]);

    const functions = fnIds
        .map(id => state.local[id])
        .filter((f): f is NetworkFunction => !!f);

    return { functions, loading, isDirty, getServerFn, dispatch, saveFn, discardFn, createFn };
};
