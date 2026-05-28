import { useCallback } from 'react';
import { API } from '../../../../api';
import type { NetworkFunction, FunctionsAction } from '../types';
import { functionsReducer, initialState } from '../reducer';
import { apiToLocal, localToApi } from '../wire';
import { useEditableEntityStore } from '../../_shared/editableEntityStore';

export interface UseFunctionsDataResult {
    functions: NetworkFunction[];
    loading: boolean;
    isDirty: (fnId: string) => boolean;
    getServerFn: (fnId: string) => NetworkFunction | null;
    dispatch: (action: FunctionsAction) => void;
    saveFn: (fnId: string) => Promise<void>;
    discardFn: (fnId: string) => void;
    createFn: (name: string) => Promise<boolean>;
    deleteFn: (fnId: string) => Promise<boolean>;
}

/**
 * Loads all network functions from the API, manages local edit state via useReducer,
 * and exposes per-function save/discard/create operations.
 */
export const useFunctionsData = (): UseFunctionsDataResult => {
    const store = useEditableEntityStore({
        reducer: functionsReducer,
        initialState,
        api: API.functions,
        getIds: resp => resp.ids ?? [],
        idName: id => id.name ?? '',
        getEntity: resp => resp.function,
        apiToLocal,
        localToApi,
        makeUpdateRequest: fn => ({ function: fn }),
        makeDeleteRequest: name => ({ id: { name } }),
        makeCreateRequest: name => ({ function: { id: { name }, chains: [] } }),
        toastPrefix: 'fn-ng',
        entityLabel: 'Function',
    });

    const getServerFn = useCallback(
        (fnId: string): NetworkFunction | null => store.getServer(fnId),
        [store.getServer],
    );

    const saveFn = useCallback(
        (fnId: string): Promise<void> => store.save(fnId),
        [store.save],
    );

    const discardFn = useCallback(
        (fnId: string): void => store.discard(fnId),
        [store.discard],
    );

    const isDirty = useCallback(
        (fnId: string): boolean => store.isDirty(fnId),
        [store.isDirty],
    );

    const createFn = useCallback(
        (name: string): Promise<boolean> => store.create(name),
        [store.create],
    );

    const deleteFn = useCallback(
        (fnId: string): Promise<boolean> => store.remove(fnId),
        [store.remove],
    );

    return {
        functions: store.entities,
        loading: store.loading,
        isDirty,
        getServerFn,
        dispatch: store.dispatch,
        saveFn,
        discardFn,
        createFn,
        deleteFn,
    };
};
