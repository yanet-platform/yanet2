import type { NetworkFunction, Chain, Module } from './types';
import type { Function as APIFunction, FunctionChain, ModuleId } from '../../../api/functions';

/** Synthesize a stable chain id from function/chain names and position. */
const makeChainId = (fnName: string, chainName: string, chainIdx: number): string =>
    `fn:${fnName}::ch:${chainName}::idx:${chainIdx}`;

/** Synthesize a stable module id from function/chain/module identity. */
const makeModuleId = (fnName: string, chainName: string, moduleIdx: number, type: string, name: string): string =>
    `fn:${fnName}::ch:${chainName}::m:${moduleIdx}::${type}/${name}`;

/** Convert a single API FunctionChain into local Chain. */
const apiChainToLocal = (fnName: string, fc: FunctionChain, chainIdx: number): Chain => {
    const chainName = fc.chain?.name ?? `chain${chainIdx}`;
    const modules: Module[] = (fc.chain?.modules ?? []).map((m: ModuleId, modIdx: number) => ({
        id: makeModuleId(fnName, chainName, modIdx, m.type ?? '', m.name ?? ''),
        type: m.type ?? '',
        name: m.name ?? '',
    }));
    return {
        id: makeChainId(fnName, chainName, chainIdx),
        name: chainName,
        weight: Number(fc.weight ?? 0),
        modules,
    };
};

/** Derive a function's display type from the first non-empty module type. */
const deriveFnType = (chains: FunctionChain[]): string => {
    for (const fc of chains) {
        for (const m of fc.chain?.modules ?? []) {
            if (m.type) {
                return m.type;
            }
        }
    }
    return 'forward';
};

/** Convert an API Function into the local NetworkFunction model. */
export const apiToLocal = (fn: APIFunction): NetworkFunction => {
    const fnName = fn.id?.name ?? '';
    const chains = (fn.chains ?? []).map((fc: FunctionChain, idx: number) => apiChainToLocal(fnName, fc, idx));
    return {
        id: fnName,
        type: deriveFnType(fn.chains ?? []),
        chains,
    };
};

/** Convert the local NetworkFunction back into API shape for save. */
export const localToApi = (fn: NetworkFunction): APIFunction => ({
    id: { name: fn.id },
    chains: fn.chains.map((c): FunctionChain => ({
        chain: {
            name: c.name,
            modules: c.modules.map((m): ModuleId => ({ type: m.type, name: m.name })),
        },
        weight: c.weight,
    })),
});
