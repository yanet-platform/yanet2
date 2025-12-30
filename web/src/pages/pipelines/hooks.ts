import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Pipeline, PipelineId } from '../../api/pipelines';
import type { FunctionId } from '../../api/common';
import type { CounterInfo, DeviceInfo } from '../../api';
import { toaster } from '../../utils';
import { useGraphEditor, useInterpolatedCounters } from '../../hooks';
import type { InterpolatedCounterData } from '../../hooks';
import type { PipelineNode, PipelineEdge } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateLinkedList } from './utils';

// Re-export formatPps and formatBps from utils for backwards compatibility
export { formatPps, formatBps } from '../../utils';

export interface UsePipelineDataResult {
    pipelineIds: PipelineId[];
    loading: boolean;
    error: string | null;
    reloadPipelines: () => Promise<void>;
    loadPipeline: (pipelineId: PipelineId) => Promise<Pipeline | null>;
    createPipeline: (name: string) => Promise<boolean>;
    updatePipeline: (pipeline: Pipeline) => Promise<boolean>;
    deletePipeline: (pipelineId: PipelineId) => Promise<boolean>;
    loadFunctionList: () => Promise<FunctionId[]>;
}

/**
 * Hook for managing pipeline data and API interactions
 */
export const usePipelineData = (): UsePipelineDataResult => {
    const [pipelineIds, setPipelineIds] = useState<PipelineId[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const loadPipelines = useCallback(async (): Promise<void> => {
        setLoading(true);
        setError(null);

        try {
            const response = await API.pipelines.list({});
            setPipelineIds(response.ids || []);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load pipelines';
            setError(message);
            toaster.error('pipelines-load-error', 'Failed to load pipelines', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        loadPipelines();
    }, [loadPipelines]);

    const loadPipeline = useCallback(async (
        pipelineId: PipelineId
    ): Promise<Pipeline | null> => {
        try {
            const response = await API.pipelines.get({ id: pipelineId });
            return response.pipeline || null;
        } catch (err) {
            toaster.error('pipeline-get-error', `Failed to load pipeline ${pipelineId.name}`, err);
            return null;
        }
    }, []);

    const createPipeline = useCallback(async (
        name: string
    ): Promise<boolean> => {
        try {
            const newPipeline: Pipeline = {
                id: { name },
                functions: [],
            };

            await API.pipelines.update({ pipeline: newPipeline });

            // Reload to get updated list
            await loadPipelines();

            toaster.success('pipeline-create-success', `Pipeline "${name}" created`);
            return true;
        } catch (err) {
            toaster.error('pipeline-create-error', `Failed to create pipeline "${name}"`, err);
            return false;
        }
    }, [loadPipelines]);

    const updatePipeline = useCallback(async (
        pipeline: Pipeline
    ): Promise<boolean> => {
        try {
            await API.pipelines.update({ pipeline });
            toaster.success('pipeline-update-success', `Pipeline "${pipeline.id?.name}" saved`);
            return true;
        } catch (err) {
            toaster.error('pipeline-update-error', `Failed to save pipeline "${pipeline.id?.name}"`, err);
            return false;
        }
    }, []);

    const deletePipeline = useCallback(async (
        pipelineId: PipelineId
    ): Promise<boolean> => {
        try {
            await API.pipelines.delete({ id: pipelineId });

            // Update local state
            setPipelineIds(prev => prev.filter(id => id.name !== pipelineId.name));

            toaster.success('pipeline-delete-success', `Pipeline "${pipelineId.name}" deleted`);
            return true;
        } catch (err) {
            toaster.error('pipeline-delete-error', `Failed to delete pipeline "${pipelineId.name}"`, err);
            return false;
        }
    }, []);

    const loadFunctionList = useCallback(async (): Promise<FunctionId[]> => {
        try {
            const response = await API.functions.list({});
            return response.ids || [];
        } catch (err) {
            toaster.error('function-list-error', 'Failed to load function list', err);
            return [];
        }
    }, []);

    return {
        pipelineIds,
        loading,
        error,
        reloadPipelines: loadPipelines,
        loadPipeline,
        createPipeline,
        updatePipeline,
        deletePipeline,
        loadFunctionList,
    };
};

export interface UsePipelineGraphResult {
    nodes: PipelineNode[];
    edges: PipelineEdge[];
    isValid: boolean;
    validationErrors: string[];
    isDirty: boolean;
    setNodes: (nodes: PipelineNode[]) => void;
    setEdges: (edges: PipelineEdge[]) => void;
    setNodesWithoutDirty: (nodes: PipelineNode[]) => void;
    setEdgesWithoutDirty: (edges: PipelineEdge[]) => void;
    updateNode: (nodeId: string, data: Record<string, unknown>) => void;
    loadFromApi: (pipeline: Pipeline) => void;
    toApi: (pipelineId: string) => Pipeline;
    reset: () => void;
    markClean: () => void;
}

/**
 * Hook for managing graph state for a single pipeline
 */
export const usePipelineGraph = (initialPipeline?: Pipeline): UsePipelineGraphResult => {
    const initialState = initialPipeline ? apiToGraph(initialPipeline) : undefined;

    const validateFn = useCallback((nodes: PipelineNode[], edges: PipelineEdge[]) => {
        const validation = validateLinkedList(nodes, edges);
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
    } = useGraphEditor<PipelineNode, PipelineEdge>({
        initialState,
        createEmptyState: createEmptyGraph,
        validate: validateFn,
    });

    const loadFromApi = useCallback((pipeline: Pipeline) => {
        const newState = apiToGraph(pipeline);
        loadState(newState);
    }, [loadState]);

    const toApi = useCallback((pipelineId: string): Pipeline => {
        return graphToApi(pipelineId, nodes, edges);
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

export interface UseFunctionCountersResult {
    counters: Map<string, InterpolatedCounterData>;
}

/**
 * Hook for fetching and interpolating function counters.
 * 
 * Uses the generic useInterpolatedCounters hook with pipeline-specific fetch logic.
 * - Polls counters every 1 second from backend
 * - Aggregates counters across all devices using the pipeline
 * - Updates visual every 30ms using linear interpolation
 */
export const useFunctionCounters = (
    pipelineName: string,
    functionNames: string[]
): UseFunctionCountersResult => {
    // Store devices that use this pipeline
    const [devices, setDevices] = useState<DeviceInfo[]>([]);

    // Fetch devices on mount
    useEffect(() => {
        const fetchDevices = async () => {
            try {
                const response = await API.inspect.inspect();
                const allDevices = response.instanceInfo?.devices ?? [];
                
                // Find devices that use this pipeline
                const matchingDevices = allDevices.filter(device => {
                    const inputPipelines = device.inputPipelines ?? [];
                    const outputPipelines = device.outputPipelines ?? [];
                    return inputPipelines.some(p => p.name === pipelineName) ||
                           outputPipelines.some(p => p.name === pipelineName);
                });
                
                setDevices(matchingDevices);
            } catch (error) {
                console.error('Failed to fetch devices for counters:', error);
            }
        };
        
        fetchDevices();
    }, [pipelineName]);

    // Create stable fetch function
    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();
        
        // Initialize with zeros
        for (const funcName of functionNames) {
            newValues.set(funcName, { packets: BigInt(0), bytes: BigInt(0) });
        }
        
        // Fetch and aggregate counters across all devices
        for (const device of devices) {
            const deviceName = device.name || '';
            
            for (const funcName of functionNames) {
                try {
                    const response = await API.counters.function({
                        device: deviceName,
                        pipeline: pipelineName,
                        function: funcName,
                    });
                    
                    // Function counters are named 'input' and 'input_bytes'
                    const rxPackets = sumCounterValues(findCounter(response.counters, 'input'));
                    const rxBytes = sumCounterValues(findCounter(response.counters, 'input_bytes'));
                    
                    const current = newValues.get(funcName)!;
                    newValues.set(funcName, {
                        packets: current.packets + rxPackets,
                        bytes: current.bytes + rxBytes,
                    });
                } catch {
                    // Ignore errors for individual function counters
                }
            }
        }
        
        return newValues;
    }, [devices, functionNames, pipelineName]);

    // Use the generic interpolated counters hook
    const { counters } = useInterpolatedCounters({
        keys: functionNames,
        fetchCounters,
        enabled: devices.length > 0 && functionNames.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
