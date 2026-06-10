import type { NetworkFunction, Chain, Module, FunctionsAction } from './types';
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

const updateModuleIn = (
    state: FunctionsState,
    fnId: string,
    moduleId: string,
    transform: (m: Module) => Module,
): FunctionsState => {
    const fn = findFn(state, fnId);
    if (!fn) {
        return state;
    }
    const updated: NetworkFunction = {
        ...fn,
        chains: fn.chains.map(c => ({
            ...c,
            modules: c.modules.map(m => (m.id === moduleId ? transform(m) : m)),
        })),
    };
    return updateFn(state, fnId, updated);
};

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
            const fromFn = findFn(state, action.fromFnId);
            const toFn = findFn(state, action.toFnId);
            if (!fromFn || !toFn) {
                return state;
            }

            const { fromChainId, toChainId, moduleId, toIdx } = action;

            if (action.fromFnId === action.toFnId && fromChainId === toChainId) {
                const updated = mapChains(fromFn, fromChainId, c => {
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
                return updateFn(state, action.fromFnId, updated);
            }

            const sourceChain = fromFn.chains.find(c => c.id === fromChainId);
            const targetChain = toFn.chains.find(c => c.id === toChainId);
            if (!sourceChain || !targetChain) {
                return state;
            }
            const fromIdx = sourceChain.modules.findIndex(m => m.id === moduleId);
            if (fromIdx === -1) {
                return state;
            }

            const movedModule = sourceChain.modules[fromIdx];
            const sourceFnNext = mapChains(fromFn, fromChainId, c => {
                const mods = [...c.modules];
                mods.splice(fromIdx, 1);
                return { ...c, modules: mods };
            });

            const targetFnBase = action.fromFnId === action.toFnId ? sourceFnNext : toFn;
            const targetFnNext = mapChains(targetFnBase, toChainId, c => {
                const mods = [...c.modules];
                mods.splice(toIdx, 0, movedModule);
                return { ...c, modules: mods };
            });

            if (action.fromFnId === action.toFnId) {
                return updateFn(state, action.fromFnId, targetFnNext);
            }

            const sourceState = updateFn(state, action.fromFnId, sourceFnNext);
            return updateFn(sourceState, action.toFnId, targetFnNext);
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

        case 'RENAME_MODULE':
            return updateModuleIn(state, action.fnId, action.moduleId, m => ({ ...m, name: action.name }));

        case 'UPDATE_MODULE_CONFIG':
            return updateModuleIn(state, action.fnId, action.moduleId, m => ({ ...m, ...action.patch }));

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
