import type { NetworkFunction, Chain, FunctionsAction } from './types';
import { localToApi } from './wire';
import type { EntityState, BaseEntityAction } from '../_shared/editableEntityStore';
import {
    createInitialEntityState,
    applyEntityUpdate,
    handleBaseEntityAction,
} from '../_shared/editableEntityStore';

export type FunctionsState = EntityState<NetworkFunction>;

export const initialState: FunctionsState = createInitialEntityState<NetworkFunction>();

const findFn = (state: FunctionsState, fnId: string): NetworkFunction | undefined =>
    state.local[fnId];

const updateFn = (state: FunctionsState, fnId: string, updated: NetworkFunction): FunctionsState =>
    applyEntityUpdate(state, fnId, updated, localToApi);

const mapChains = (fn: NetworkFunction, chainId: string, mapper: (c: Chain) => Chain): NetworkFunction => ({
    ...fn,
    chains: fn.chains.map(c => (c.id === chainId ? mapper(c) : c)),
});

export const functionsReducer = (
    state: FunctionsState,
    action: FunctionsAction | BaseEntityAction<NetworkFunction>,
): FunctionsState => {
    switch (action.type) {
        case 'LOAD_ENTITY':
        case 'ADD_ENTITY':
        case 'REMOVE_ENTITY':
            return handleBaseEntityAction(state, action as BaseEntityAction<NetworkFunction>);

        case 'MOVE_MODULE': {
            const fn = findFn(state, action.fnId);
            if (!fn) {
                return state;
            }

            const { fromChainId, toChainId, moduleId, toIdx } = action;

            if (fromChainId === toChainId) {
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
                    const insertAt = fromIdx < toIdx ? toIdx - 1 : toIdx;
                    mods.splice(insertAt, 0, moved);
                    return { ...c, modules: mods };
                });
                return updateFn(state, action.fnId, updated);
            }

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

        default:
            return state;
    }
};
