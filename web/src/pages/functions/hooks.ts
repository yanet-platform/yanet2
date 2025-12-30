import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Function as APIFunction } from '../../api/functions';
import type { FunctionId } from '../../api/common';
import type { CounterInfo, DeviceInfo } from '../../api';
import { toaster } from '../../utils';
import { useGraphEditor, useInterpolatedCounters } from '../../hooks';
import type { InterpolatedCounterData } from '../../hooks';
import type { FunctionNode, FunctionEdge } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateGraph } from './utils';

// Re-export formatPps and formatBps from utils for convenience
export { formatPps, formatBps } from '../../utils';

export type FunctionMap = Record<string, APIFunction>;

export interface UseFunctionDataResult {
    functionIds: FunctionId[];
    loading: boolean;
    functionsLoading: boolean;
    functions: FunctionMap;
    error: string | null;
    reloadFunctions: () => Promise<void>;
    loadFunction: (functionId: FunctionId) => Promise<APIFunction | null>;
    createFunction: (name: string) => Promise<boolean>;
    updateFunction: (func: APIFunction) => Promise<boolean>;
    deleteFunction: (functionId: FunctionId) => Promise<boolean>;
}

/**
 * Hook for managing function data and API interactions
 */
export const useFunctionData = (): UseFunctionDataResult => {
    const [functionIds, setFunctionIds] = useState<FunctionId[]>([]);
    const [listLoading, setListLoading] = useState(true);
    const [functionsLoading, setFunctionsLoading] = useState(true);
    const [functions, setFunctions] = useState<FunctionMap>({});
    const [error, setError] = useState<string | null>(null);

    const loadFunctions = useCallback(async (): Promise<void> => {
        setListLoading(true);
        setFunctionsLoading(true);
        setError(null);

        try {
            // Load function list
            const response = await API.functions.list({});
            const ids = response.ids || [];
            setFunctionIds(ids);

            // Preload functions to avoid per-card loading flashes
            const loadedFunctions: FunctionMap = {};

            await Promise.all(ids.map(async (functionId) => {
                try {
                    const response = await API.functions.get({ id: functionId });
                    const func = response.function;
                    const key = func?.id?.name || functionId.name;
                    if (func && key) {
                        loadedFunctions[key] = func;
                    }
                } catch (err) {
                    toaster.error('function-get-error', `Failed to load function ${functionId.name}`, err);
                }
            }));

            setFunctions(loadedFunctions);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load functions';
            setError(message);
            toaster.error('functions-load-error', 'Failed to load functions', err);
        } finally {
            setListLoading(false);
            setFunctionsLoading(false);
        }
    }, []);

    useEffect(() => {
        loadFunctions();
    }, [loadFunctions]);

    const loadFunction = useCallback(async (
        functionId: FunctionId
    ): Promise<APIFunction | null> => {
        const cached = functions[functionId.name || ''];
        if (cached) {
            return cached;
        }

        try {
            const response = await API.functions.get({ id: functionId });
            const func = response.function || null;

            const funcName = func?.id?.name;
            if (func && funcName) {
                setFunctions(prev => ({
                    ...prev,
                    [funcName]: func,
                }));
            }

            return func;
        } catch (err) {
            toaster.error('function-get-error', `Failed to load function ${functionId.name}`, err);
            return null;
        }
    }, [functions]);

    const createFunction = useCallback(async (
        name: string
    ): Promise<boolean> => {
        try {
            const newFunction: APIFunction = {
                id: { name },
                chains: [],
            };

            await API.functions.update({ function: newFunction });

            // Reload to get updated list
            await loadFunctions();

            toaster.success('function-create-success', `Function "${name}" created`);
            return true;
        } catch (err) {
            toaster.error('function-create-error', `Failed to create function "${name}"`, err);
            return false;
        }
    }, [loadFunctions]);

    const updateFunction = useCallback(async (
        func: APIFunction
    ): Promise<boolean> => {
        try {
            await API.functions.update({ function: func });
            toaster.success('function-update-success', `Function "${func.id?.name}" saved`);

            const funcName = func.id?.name;
            if (funcName) {
                setFunctions(prev => ({
                    ...prev,
                    [funcName]: func,
                }));
            }

            return true;
        } catch (err) {
            toaster.error('function-update-error', `Failed to save function "${func.id?.name}"`, err);
            return false;
        }
    }, []);

    const deleteFunction = useCallback(async (
        functionId: FunctionId
    ): Promise<boolean> => {
        try {
            await API.functions.delete({ id: functionId });

            // Update local state
            setFunctionIds(prev => prev.filter(id => id.name !== functionId.name));

            const functionName = functionId.name;
            if (functionName) {
                setFunctions(prev => {
                    const { [functionName]: _removed, ...rest } = prev;
                    return rest;
                });
            }

            toaster.success('function-delete-success', `Function "${functionId.name}" deleted`);
            return true;
        } catch (err) {
            toaster.error('function-delete-error', `Failed to delete function "${functionId.name}"`, err);
            return false;
        }
    }, []);

    const loading = listLoading || functionsLoading;

    return {
        functionIds,
        loading,
        functionsLoading,
        functions,
        error,
        reloadFunctions: loadFunctions,
        loadFunction,
        createFunction,
        updateFunction,
        deleteFunction,
    };
};

export interface UseFunctionGraphResult {
    nodes: FunctionNode[];
    edges: FunctionEdge[];
    isValid: boolean;
    validationErrors: string[];
    isDirty: boolean;
    setNodes: (nodes: FunctionNode[]) => void;
    setEdges: (edges: FunctionEdge[]) => void;
    setNodesWithoutDirty: (nodes: FunctionNode[]) => void;
    setEdgesWithoutDirty: (edges: FunctionEdge[]) => void;
    updateNode: (nodeId: string, data: Record<string, unknown>) => void;
    loadFromApi: (func: APIFunction) => void;
    toApi: (functionId: string) => APIFunction;
    reset: () => void;
    markClean: () => void;
}

/**
 * Hook for managing graph state for a single function
 */
export const useFunctionGraph = (initialFunction?: APIFunction): UseFunctionGraphResult => {
    const initialState = initialFunction ? apiToGraph(initialFunction) : undefined;

    const validateFn = useCallback((nodes: FunctionNode[], edges: FunctionEdge[]) => {
        const validation = validateGraph(nodes, edges);
        return validation.errors;
    }, []);

    const {
        nodes,
        edges,
        isValid,
        validationErrors,
        isDirty,
        setNodes,
        setEdges,
        setNodesWithoutDirty,
        setEdgesWithoutDirty,
        updateNode,
        loadState,
        reset,
        markClean,
    } = useGraphEditor<FunctionNode, FunctionEdge>({
        initialState,
        createEmptyState: createEmptyGraph,
        validate: validateFn,
    });

    const loadFromApi = useCallback((func: APIFunction) => {
        const newState = apiToGraph(func);
        loadState(newState);
    }, [loadState]);

    const toApi = useCallback((functionId: string): APIFunction => {
        return graphToApi(functionId, nodes, edges);
    }, [nodes, edges]);

    return {
        nodes,
        edges,
        isValid,
        validationErrors,
        isDirty,
        setNodes,
        setEdges,
        setNodesWithoutDirty,
        setEdgesWithoutDirty,
        updateNode,
        loadFromApi,
        toApi,
        reset,
        markClean,
    };
};

// Helper to sum counter values across all instances and all values within each instance
const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) return BigInt(0);
    return counter.instances.reduce((sum, inst) => {
        // Sum all values in the instance (e.g., values from different workers/cores)
        const instSum = (inst.values ?? []).reduce((s, val) => s + BigInt(val ?? 0), BigInt(0));
        return sum + instSum;
    }, BigInt(0));
};

// Helper to find counter by name
const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined => {
    return counters?.find(c => c.name === name);
};

// Module info for counter fetching
export interface ModuleInfo {
    nodeId: string;
    chainName: string;
    moduleType: string;
    moduleName: string;
}

export interface UseModuleCountersResult {
    // Map from nodeId to counter data
    counters: Map<string, InterpolatedCounterData>;
}

/**
 * Hook for fetching and interpolating module counters.
 * 
 * Uses the generic useInterpolatedCounters hook with module-specific fetch logic.
 * - Polls module counters every 1 second from backend using the Module API
 * - Aggregates counters across all devices and pipelines using the function
 * - Updates visual every 30ms using linear interpolation
 * 
 * @param functionName - The function name
 * @param moduleInfoList - Array of module info objects with chain, type, and name
 */
export const useModuleCounters = (
    functionName: string,
    moduleInfoList: ModuleInfo[]
): UseModuleCountersResult => {
    // Store devices that use this function (via pipelines)
    const [devices, setDevices] = useState<DeviceInfo[]>([]);
    
    // Store pipeline names that use this function
    const [pipelineNames, setPipelineNames] = useState<string[]>([]);

    // Fetch devices and pipelines on mount
    useEffect(() => {
        const fetchDevicesAndPipelines = async () => {
            try {
                const response = await API.inspect.inspect();
                const instanceInfo = response.instanceInfo;
                const allDevices = instanceInfo?.devices ?? [];
                const allPipelines = instanceInfo?.pipelines ?? [];
                
                // Find pipelines that use this function
                const matchingPipelines = allPipelines.filter(p => {
                    const funcs = p.functions ?? [];
                    return funcs.includes(functionName);
                });
                
                const pipelineNamesSet = new Set(matchingPipelines.map(p => p.name).filter((n): n is string => !!n));
                
                // Find devices that use these pipelines
                const matchingDevices: DeviceInfo[] = [];
                for (const device of allDevices) {
                    const inputPipelines = device.inputPipelines ?? [];
                    const outputPipelines = device.outputPipelines ?? [];
                    const allDevicePipelines = [...inputPipelines, ...outputPipelines];
                    
                    for (const pipeline of allDevicePipelines) {
                        if (pipeline.name && pipelineNamesSet.has(pipeline.name)) {
                            if (!matchingDevices.includes(device)) {
                                matchingDevices.push(device);
                            }
                        }
                    }
                }
                
                setDevices(matchingDevices);
                setPipelineNames(Array.from(pipelineNamesSet));
            } catch (error) {
                console.error('Failed to fetch devices for counters:', error);
            }
        };
        
        fetchDevicesAndPipelines();
    }, [functionName]);

    // Extract nodeIds for the interpolation hook keys
    const nodeIds = moduleInfoList.map(m => m.nodeId);

    // Create stable fetch function
    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();
        
        // Initialize with zeros
        for (const moduleInfo of moduleInfoList) {
            newValues.set(moduleInfo.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
        }
        
        // Fetch module counters for each module
        for (const device of devices) {
            const deviceName = device.name || '';
            
            for (const pipelineName of pipelineNames) {
                for (const moduleInfo of moduleInfoList) {
                    try {
                        const response = await API.counters.module({
                            device: deviceName,
                            pipeline: pipelineName,
                            function: functionName,
                            chain: moduleInfo.chainName,
                            moduleType: moduleInfo.moduleType,
                            moduleName: moduleInfo.moduleName,
                        });
                        
                        // Module counters are named 'rx' and 'rx_bytes'
                        const rxPackets = sumCounterValues(findCounter(response.counters, 'rx'));
                        const rxBytes = sumCounterValues(findCounter(response.counters, 'rx_bytes'));
                        
                        const current = newValues.get(moduleInfo.nodeId)!;
                        newValues.set(moduleInfo.nodeId, {
                            packets: current.packets + rxPackets,
                            bytes: current.bytes + rxBytes,
                        });
                    } catch {
                        // Ignore errors for individual module counters
                    }
                }
            }
        }
        
        return newValues;
    }, [devices, pipelineNames, functionName, moduleInfoList]);

    // Use the generic interpolated counters hook
    const { counters } = useInterpolatedCounters({
        keys: nodeIds,
        fetchCounters,
        enabled: devices.length > 0 && pipelineNames.length > 0 && moduleInfoList.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
