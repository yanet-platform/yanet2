import type { NetworkFunction, Chain, FunctionsAction } from './types';

export interface FunctionsState {
    /** Server-authoritative snapshots, keyed by fn id. */
    server: Record<string, NetworkFunction>;
    /** Local edited state, keyed by fn id. */
    local: Record<string, NetworkFunction>;
    /** Which fn ids have unsaved edits. */
    dirty: Set<string>;
}

export const initialState: FunctionsState = {
    server: {},
    local: {},
    dirty: new Set(),
};

const findFn = (state: FunctionsState, fnId: string): NetworkFunction | undefined =>
    state.local[fnId];

const updateFn = (state: FunctionsState, fnId: string, updated: NetworkFunction): FunctionsState => ({
    ...state,
    local: { ...state.local, [fnId]: updated },
    dirty: new Set([...state.dirty, fnId]),
});

const mapChains = (fn: NetworkFunction, chainId: string, mapper: (c: Chain) => Chain): NetworkFunction => ({
    ...fn,
    chains: fn.chains.map(c => (c.id === chainId ? mapper(c) : c)),
});

export const functionsReducer = (state: FunctionsState, action: FunctionsAction): FunctionsState => {
    switch (action.type) {
        case 'LOAD_FUNCTION': {
            const fn = action.fn;
            const dirty = new Set(state.dirty);
            dirty.delete(fn.id);
            return {
                ...state,
                server: { ...state.server, [fn.id]: fn },
                local: { ...state.local, [fn.id]: fn },
                dirty,
            };
        }

        case 'MOVE_MODULE': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }

            const { fromChainId, toChainId, moduleId, toIdx } = action;

            if (fromChainId === toChainId) {
                // Same-chain reorder.
                // proof: after splicing the element out at fromIdx, the array shrinks by 1.
                // If toIdx > fromIdx, the intended slot is now at toIdx - 1.
                // Example: [A, B, C, D], move A (idx 0) to after D (toIdx 3) → splice out A → [B, C, D],
                //   insert at 3-1=2 → [B, C, A] ✓ wait that's wrong example; let's redo:
                //   move idx 0 → toIdx 3 means "insert before position 3" in the original array.
                //   After splicing 0: [B, C, D]. Insert before new position 2 (=3-1) → [B, C, A, D]?
                //   No: "drop at slot 3" means append after the 3rd element, so final [B, C, D, A].
                //   splice insert at index 2: [B, C, A, D] — that is insert BEFORE D.
                //   To get [B, C, D, A] we need insert at index 3. So: toIdx > fromIdx → toIdx stays same.
                //   Actually the standard pattern: remove source, then insert at adjusted position.
                //   If fromIdx < toIdx: after removing the source, the target index decreases by 1.
                //   So actualInsertIdx = toIdx - 1 when fromIdx < toIdx.
                const updated = mapChains(fn, fromChainId, c => {
                    const fromIdx = c.modules.findIndex(m => m.id === moduleId);
                    if (fromIdx === -1) {
                        return c;
                    }
                    if (fromIdx === toIdx || fromIdx === toIdx - 1) {
                        return c;
                    }
                    const mods = [...c.modules];
                    const [moved] = mods.splice(fromIdx, 1);
                    // proof: fromIdx < toIdx → insert position after removal = toIdx - 1.
                    //        fromIdx > toIdx → insert position unchanged = toIdx.
                    const insertAt = fromIdx < toIdx ? toIdx - 1 : toIdx;
                    mods.splice(insertAt, 0, moved);
                    return { ...c, modules: mods };
                });
                return updateFn(state, action.fnId, updated);
            }

            // Cross-chain move within same function.
            let movedModule = null as typeof fn.chains[0]['modules'][0] | null;
            const afterRemove = mapChains(fn, fromChainId, c => {
                const fromIdx = c.modules.findIndex(m => m.id === moduleId);
                if (fromIdx === -1) {
                    return c;
                }
                const mods = [...c.modules];
                [movedModule] = mods.splice(fromIdx, 1);
                return { ...c, modules: mods };
            });

            if (!movedModule) {
                return state;
            }

            const capturedModule = movedModule;
            const afterInsert = mapChains(afterRemove, toChainId, c => {
                const mods = [...c.modules];
                mods.splice(toIdx, 0, capturedModule);
                return { ...c, modules: mods };
            });

            return updateFn(state, action.fnId, afterInsert);
        }

        case 'ADD_MODULE': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated = mapChains(fn, action.chainId, c => {
                const mods = [...c.modules];
                mods.splice(action.toIdx, 0, action.module);
                return { ...c, modules: mods };
            });
            return updateFn(state, action.fnId, updated);
        }

        case 'REMOVE_MODULE': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated = mapChains(fn, action.chainId, c => ({
                ...c,
                modules: c.modules.filter(m => m.id !== action.moduleId),
            }));
            return updateFn(state, action.fnId, updated);
        }

        case 'RENAME_MODULE': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated: NetworkFunction = {
                ...fn,
                chains: fn.chains.map(c => ({
                    ...c,
                    modules: c.modules.map(m =>
                        m.id === action.moduleId ? { ...m, name: action.name } : m
                    ),
                })),
            };
            return updateFn(state, action.fnId, updated);
        }

        case 'UPDATE_MODULE_CONFIG': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated: NetworkFunction = {
                ...fn,
                chains: fn.chains.map(c => ({
                    ...c,
                    modules: c.modules.map(m =>
                        m.id === action.moduleId ? { ...m, ...action.patch } : m
                    ),
                })),
            };
            return updateFn(state, action.fnId, updated);
        }

        case 'UPDATE_CHAIN': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated = mapChains(fn, action.chainId, c => ({ ...c, ...action.patch }));
            return updateFn(state, action.fnId, updated);
        }

        case 'ADD_CHAIN': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const chains = [...fn.chains];
            const insertAt = action.toIdx !== undefined ? action.toIdx : chains.length;
            chains.splice(insertAt, 0, action.chain);
            return updateFn(state, action.fnId, { ...fn, chains });
        }

        case 'REMOVE_CHAIN': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }
            const updated: NetworkFunction = {
                ...fn,
                chains: fn.chains.filter(c => c.id !== action.chainId),
            };
            return updateFn(state, action.fnId, updated);
        }

        case 'ADD_FUNCTION': {
            const fn = action.fn;
            return {
                ...state,
                server: { ...state.server, [fn.id]: fn },
                local: { ...state.local, [fn.id]: fn },
            };
        }

        case 'REMOVE_FUNCTION': {
            const { [action.fnId]: _s, ...serverRest } = state.server;
            const { [action.fnId]: _l, ...localRest } = state.local;
            const dirty = new Set(state.dirty);
            dirty.delete(action.fnId);
            return { server: serverRest, local: localRest, dirty };
        }

        default:
            return state;
    }
};

/** Mark a function as clean (after successful save). */
export const markClean = (state: FunctionsState, fnId: string): FunctionsState => {
    const dirty = new Set(state.dirty);
    dirty.delete(fnId);
    return {
        ...state,
        server: { ...state.server, [fnId]: state.local[fnId] },
        dirty,
    };
};

/** Discard local edits for a function, reverting to server snapshot. */
export const discardEdits = (state: FunctionsState, fnId: string): FunctionsState => {
    const server = state.server[fnId];
    if (!server) {
        return state;
    }
    const dirty = new Set(state.dirty);
    dirty.delete(fnId);
    return {
        ...state,
        local: { ...state.local, [fnId]: server },
        dirty,
    };
};
