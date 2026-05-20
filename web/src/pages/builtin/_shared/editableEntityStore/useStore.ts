import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { toaster } from '../../../../utils';
import type { EntityState, BaseEntityAction } from './reducer';

/** API surface required by the generic store. */
export interface EntityStoreApi<I, L, G> {
    /** List all entity ids. */
    list: (req: Record<string, never>) => Promise<L>;
    /** Fetch a single entity by id. */
    get: (req: { id: I }) => Promise<G>;
    /** Create or update an entity. */
    update: (req: Record<string, unknown>) => Promise<unknown>;
    /** Delete an entity by id. */
    delete: (req: { id: I }) => Promise<unknown>;
}

/** Configuration passed to useEditableEntityStore. */
export interface EditableEntityStoreConfig<T, W, I, L, G, A> {
    /** Reducer that handles both module-specific actions and base entity actions. */
    reducer: (state: EntityState<T>, action: A | BaseEntityAction<T>) => EntityState<T>;
    /** Initial state value. */
    initialState: EntityState<T>;
    /** API module for CRUD operations. */
    api: EntityStoreApi<I, L, G>;
    /** Extract entity ids from a list response. */
    getIds: (resp: L) => I[];
    /** Extract entity name/key from an id object. */
    idName: (id: I) => string;
    /** Extract entity from a get response (may be undefined when not found). */
    getEntity: (resp: G) => W | undefined;
    /** Convert API entity to local form. */
    apiToLocal: (api: W) => T;
    /** Convert local entity to API form for update. */
    localToApi: (local: T) => W;
    /** Build an update request body from the API entity. */
    makeUpdateRequest: (api: W) => Record<string, unknown>;
    /** Build a delete request body from the entity name. */
    makeDeleteRequest: (name: string) => { id: I };
    /** Build the empty create request body (used for new entity creation). */
    makeCreateRequest: (name: string) => Record<string, unknown>;
    /** Toast key prefix (e.g. "fn-ng" or "pl"). */
    toastPrefix: string;
    /** Human-readable entity label (e.g. "Function" or "Pipeline"). */
    entityLabel: string;
}

/** Public surface returned by useEditableEntityStore. */
export interface EditableEntityStoreResult<T, A> {
    entities: T[];
    loading: boolean;
    isDirty: (id: string) => boolean;
    getServer: (id: string) => T | null;
    dispatch: (action: A) => void;
    save: (id: string) => Promise<void>;
    discard: (id: string) => void;
    create: (name: string) => Promise<boolean>;
    remove: (id: string) => Promise<boolean>;
    reload: () => Promise<void>;
}

/**
 * Generic hook for editable entity stores (functions, pipelines).
 * Manages list-load, parallel-get, and CRUD operations, delegating
 * state shape to the provided reducer.
 */
export const useEditableEntityStore = <T, W, I, L, G, A>(
    config: EditableEntityStoreConfig<T, W, I, L, G, A>,
): EditableEntityStoreResult<T, A> => {
    const configRef = useRef(config);
    configRef.current = config;

    const { reducer, initialState } = config;

    const [state, rawDispatch] = useReducer(
        reducer as (state: EntityState<T>, action: A | BaseEntityAction<T>) => EntityState<T>,
        initialState,
    );
    const [loading, setLoading] = useState(true);
    const [entityIds, setEntityIds] = useState<string[]>([]);

    const dispatch = useCallback((action: A) => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        const { api, getIds, idName, getEntity, apiToLocal, toastPrefix, entityLabel } = configRef.current;
        setLoading(true);
        try {
            const listResp = await api.list({} as Record<string, never>);
            const ids = getIds(listResp);
            const names = ids.map(id => idName(id)).filter(Boolean);
            setEntityIds(names);

            await Promise.all(ids.map(async (entityId) => {
                try {
                    const resp = await api.get({ id: entityId });
                    const apiEntity = getEntity(resp);
                    if (apiEntity) {
                        const local = apiToLocal(apiEntity);
                        rawDispatch({
                            type: 'LOAD_ENTITY',
                            entity: local,
                            id: idName(entityId),
                        } as BaseEntityAction<T>);
                    }
                } catch (err) {
                    toaster.error(
                        `${toastPrefix}-load`,
                        `Failed to load ${entityLabel.toLowerCase()} ${idName(entityId)}`,
                        err,
                    );
                }
            }));
        } catch (err) {
            toaster.error(`${toastPrefix}-list`, `Failed to load ${entityLabel.toLowerCase()}s`, err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const save = useCallback(async (id: string): Promise<void> => {
        const { api, makeUpdateRequest, localToApi, toastPrefix, entityLabel } = configRef.current;
        const entity = state.local[id];
        if (!entity) {
            return;
        }
        try {
            await api.update(makeUpdateRequest(localToApi(entity)));
            rawDispatch({ type: 'LOAD_ENTITY', entity, id } as BaseEntityAction<T>);
            toaster.success(`${toastPrefix}-save-${id}`, `${entityLabel} "${id}" saved.`);
        } catch (err) {
            toaster.error(
                `${toastPrefix}-save-err-${id}`,
                `Failed to save "${id}"`,
                err,
            );
        }
    }, [state.local]);

    const discard = useCallback((id: string): void => {
        const server = state.server[id];
        if (server) {
            rawDispatch({ type: 'LOAD_ENTITY', entity: server, id } as BaseEntityAction<T>);
        }
    }, [state.server]);

    const isDirty = useCallback((id: string): boolean => state.dirty.has(id), [state.dirty]);

    const getServer = useCallback((id: string): T | null =>
        state.server[id] ?? null, [state.server]);

    const create = useCallback(async (name: string): Promise<boolean> => {
        const { api, makeCreateRequest, toastPrefix, entityLabel } = configRef.current;
        try {
            await api.update(makeCreateRequest(name));
            await load();
            toaster.success(`${toastPrefix}-create-${name}`, `${entityLabel} "${name}" created.`);
            return true;
        } catch (err) {
            toaster.error(
                `${toastPrefix}-create-err-${name}`,
                `Failed to create "${name}"`,
                err,
            );
            return false;
        }
    }, [load]);

    const remove = useCallback(async (id: string): Promise<boolean> => {
        const { api, makeDeleteRequest, toastPrefix, entityLabel } = configRef.current;
        try {
            await api.delete(makeDeleteRequest(id));
            rawDispatch({ type: 'REMOVE_ENTITY', id } as BaseEntityAction<T>);
            setEntityIds(prev => prev.filter(eid => eid !== id));
            toaster.success(`${toastPrefix}-delete-${id}`, `${entityLabel} "${id}" deleted.`);
            return true;
        } catch (err) {
            toaster.error(
                `${toastPrefix}-delete-err-${id}`,
                `Failed to delete "${id}"`,
                err,
            );
            return false;
        }
    }, []);

    const entities = useMemo(
        () => entityIds.map(id => state.local[id]).filter((e): e is T => e !== undefined),
        [entityIds, state.local],
    );

    return { entities, loading, isDirty, getServer, dispatch, save, discard, create, remove, reload: load };
};
