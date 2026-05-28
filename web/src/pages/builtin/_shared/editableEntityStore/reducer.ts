/** Generic editable-entity store: state shape, base action types, and helpers. */

/** Shared state shape for any editable entity store. */
export interface EntityState<T> {
    /** Server-authoritative snapshots, keyed by entity id. */
    server: Record<string, T>;
    /** Local edited state, keyed by entity id. */
    local: Record<string, T>;
    /** Which entity ids have unsaved edits. */
    dirty: Set<string>;
}

/** Base action types handled by the generic reducer. */
export type BaseEntityAction<T> =
    | { type: 'LOAD_ENTITY';    entity: T; id: string }
    | { type: 'ADD_ENTITY';     entity: T; id: string }
    | { type: 'REMOVE_ENTITY';  id: string };

/** Returns the initial empty state for an entity store. */
export const createInitialEntityState = <T>(): EntityState<T> => ({
    server: {},
    local: {},
    dirty: new Set(),
});

/**
 * Returns true when the local entity differs from the server snapshot,
 * using JSON-serialised API form for comparison.
 */
export const computeIsDirty = <T, A>(
    local: T,
    server: T | undefined,
    localToApi: (entity: T) => A,
): boolean => {
    if (!server) {
        return true;
    }
    return JSON.stringify(localToApi(local)) !== JSON.stringify(localToApi(server));
};

/**
 * Applies a local update to an entity, recomputing the dirty flag.
 * Returns a new state object (never mutates).
 */
export const applyEntityUpdate = <T, A>(
    state: EntityState<T>,
    id: string,
    updated: T,
    localToApi: (entity: T) => A,
): EntityState<T> => {
    const dirty = new Set(state.dirty);
    if (computeIsDirty(updated, state.server[id], localToApi)) {
        dirty.add(id);
    } else {
        dirty.delete(id);
    }
    return {
        ...state,
        local: { ...state.local, [id]: updated },
        dirty,
    };
};

/**
 * Handles the three base entity actions (LOAD_ENTITY, ADD_ENTITY, REMOVE_ENTITY).
 * Returns `null` when the action type is not one of the base actions, so the
 * caller can fall through to module-specific handling.
 */
export const handleBaseEntityAction = <T>(
    state: EntityState<T>,
    action: BaseEntityAction<T>,
): EntityState<T> => {
    switch (action.type) {
        case 'LOAD_ENTITY': {
            const dirty = new Set(state.dirty);
            dirty.delete(action.id);
            return {
                ...state,
                server: { ...state.server, [action.id]: action.entity },
                local: { ...state.local, [action.id]: action.entity },
                dirty,
            };
        }

        case 'ADD_ENTITY': {
            return {
                ...state,
                server: { ...state.server, [action.id]: action.entity },
                local: { ...state.local, [action.id]: action.entity },
            };
        }

        case 'REMOVE_ENTITY': {
            const { [action.id]: _s, ...serverRest } = state.server;
            const { [action.id]: _l, ...localRest } = state.local;
            const dirty = new Set(state.dirty);
            dirty.delete(action.id);
            return { server: serverRest, local: localRest, dirty };
        }
    }
};
