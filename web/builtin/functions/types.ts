export type { DragPayload } from '../_shared/lane-editor';

export type ModuleType =
    | 'route' | 'pdump' | 'acl' | 'decap' | 'nat64'
    | 'balancer' | 'forward' | string;

export type ModuleStatus = 'ok' | 'hot' | 'error' | 'unknown';

export interface Module {
    id: string;
    type: ModuleType;
    name: string;
    config?: Record<string, unknown>;
}

export interface Chain {
    id: string;
    name: string;
    weight: number;
    modules: Module[];
}

export type FunctionType = ModuleType;

export interface NetworkFunction {
    id: string;
    type: FunctionType;
    description?: string;
    chains: Chain[];
}

export interface Counters {
    pps: number;
    bps: number;
    latencyP50?: number;
    latencyP99?: number;
    drops?: number;
    status: ModuleStatus;
    history?: number[];
}

export type CountersByModuleId = Record<string, Counters>;

export type FunctionsAction =
    | {
        type: 'MOVE_MODULE';
        fromFnId: string;
        toFnId: string;
        fromChainId: string;
        toChainId: string;
        moduleId: string;
        toIdx: number;
    }
    | { type: 'ADD_MODULE';           fnId: string; chainId: string; toIdx: number; module: Module }
    | { type: 'REMOVE_MODULE';        fnId: string; chainId: string; moduleId: string }
    | { type: 'RENAME_MODULE';        fnId: string; moduleId: string; name: string }
    | { type: 'UPDATE_MODULE_CONFIG'; fnId: string; moduleId: string; patch: Partial<Module> }
    | { type: 'UPDATE_CHAIN';         fnId: string; chainId: string; patch: Partial<Chain> }
    | { type: 'ADD_CHAIN';            fnId: string; chain: Chain; toIdx?: number }
    | { type: 'REMOVE_CHAIN';         fnId: string; chainId: string };
