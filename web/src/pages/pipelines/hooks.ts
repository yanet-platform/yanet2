import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Pipeline, PipelineId } from '../../api/pipelines';
import type { FunctionId } from '../../api/common';
import type { InspectResponse } from '../../api/inspect';
import { toaster } from '../../utils';
import type { PipelineNode, PipelineEdge, PipelineGraphState } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateLinkedList } from './utils';

export interface InstanceData {
    instance: number;
    pipelineIds: PipelineId[];
}

export interface UsePipelineDataResult {
    instances: InstanceData[];
    loading: boolean;
    error: string | null;
    reloadInstances: () => Promise<void>;
    loadPipeline: (instance: number, pipelineId: PipelineId) => Promise<Pipeline | null>;
    createPipeline: (instance: number, name: string) => Promise<boolean>;
    updatePipeline: (instance: number, pipeline: Pipeline) => Promise<boolean>;
    deletePipeline: (instance: number, pipelineId: PipelineId) => Promise<boolean>;
    loadFunctionList: (instance: number) => Promise<FunctionId[]>;
}

/**
 * Hook for managing pipeline data and API interactions
 */
export const usePipelineData = (): UsePipelineDataResult => {
    const [instances, setInstances] = useState<InstanceData[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const loadInstancesAndPipelines = useCallback(async (): Promise<void> => {
        setLoading(true);
        setError(null);

        try {
            // First get all instances from inspect
            const inspectResponse: InspectResponse = await API.inspect.inspect();
            const instanceIndices = inspectResponse.instanceIndices || [];

            // Then load pipeline list for each instance
            const instanceDataPromises = instanceIndices.map(async (instanceIdx) => {
                try {
                    const response = await API.pipelines.list({ instance: instanceIdx });
                    return {
                        instance: instanceIdx,
                        pipelineIds: response.ids || [],
                    };
                } catch (err) {
                    console.error(`Failed to load pipelines for instance ${instanceIdx}:`, err);
                    return {
                        instance: instanceIdx,
                        pipelineIds: [],
                    };
                }
            });

            const instanceData = await Promise.all(instanceDataPromises);
            setInstances(instanceData);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load instances';
            setError(message);
            toaster.error('pipelines-load-error', 'Failed to load pipelines', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInstancesAndPipelines();
    }, [loadInstancesAndPipelines]);

    const loadPipeline = useCallback(async (
        instance: number,
        pipelineId: PipelineId
    ): Promise<Pipeline | null> => {
        try {
            const response = await API.pipelines.get({ instance, id: pipelineId });
            return response.pipeline || null;
        } catch (err) {
            toaster.error('pipeline-get-error', `Failed to load pipeline ${pipelineId.name}`, err);
            return null;
        }
    }, []);

    const createPipeline = useCallback(async (
        instance: number,
        name: string
    ): Promise<boolean> => {
        try {
            const newPipeline: Pipeline = {
                id: { name },
                functions: [],
            };

            await API.pipelines.update({ instance, pipeline: newPipeline });

            // Reload to get updated list
            await loadInstancesAndPipelines();

            toaster.success('pipeline-create-success', `Pipeline "${name}" created`);
            return true;
        } catch (err) {
            toaster.error('pipeline-create-error', `Failed to create pipeline "${name}"`, err);
            return false;
        }
    }, [loadInstancesAndPipelines]);

    const updatePipeline = useCallback(async (
        instance: number,
        pipeline: Pipeline
    ): Promise<boolean> => {
        try {
            await API.pipelines.update({ instance, pipeline });
            toaster.success('pipeline-update-success', `Pipeline "${pipeline.id?.name}" saved`);
            return true;
        } catch (err) {
            toaster.error('pipeline-update-error', `Failed to save pipeline "${pipeline.id?.name}"`, err);
            return false;
        }
    }, []);

    const deletePipeline = useCallback(async (
        instance: number,
        pipelineId: PipelineId
    ): Promise<boolean> => {
        try {
            await API.pipelines.delete({ instance, id: pipelineId });

            // Update local state
            setInstances(prev => prev.map(inst => {
                if (inst.instance === instance) {
                    return {
                        ...inst,
                        pipelineIds: inst.pipelineIds.filter(
                            id => id.name !== pipelineId.name
                        ),
                    };
                }
                return inst;
            }));

            toaster.success('pipeline-delete-success', `Pipeline "${pipelineId.name}" deleted`);
            return true;
        } catch (err) {
            toaster.error('pipeline-delete-error', `Failed to delete pipeline "${pipelineId.name}"`, err);
            return false;
        }
    }, []);

    const loadFunctionList = useCallback(async (instance: number): Promise<FunctionId[]> => {
        try {
            const response = await API.functions.list({ instance });
            return response.ids || [];
        } catch (err) {
            toaster.error('function-list-error', 'Failed to load function list', err);
            return [];
        }
    }, []);

    return {
        instances,
        loading,
        error,
        reloadInstances: loadInstancesAndPipelines,
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
    const [graphState, setGraphState] = useState<PipelineGraphState>(() => {
        if (initialPipeline) {
            return apiToGraph(initialPipeline);
        }
        return createEmptyGraph();
    });
    const [isDirty, setIsDirty] = useState(false);
    const [originalState, setOriginalState] = useState<PipelineGraphState | null>(null);

    const validation = validateLinkedList(graphState.nodes, graphState.edges);

    const setNodes = useCallback((nodes: PipelineNode[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
        setIsDirty(true);
    }, []);

    const setNodesWithoutDirty = useCallback((nodes: PipelineNode[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
    }, []);

    const setEdges = useCallback((edges: PipelineEdge[]) => {
        setGraphState(prev => ({ ...prev, edges }));
        setIsDirty(true);
    }, []);

    const setEdgesWithoutDirty = useCallback((edges: PipelineEdge[]) => {
        setGraphState(prev => ({ ...prev, edges }));
    }, []);

    const updateNode = useCallback((nodeId: string, data: Record<string, unknown>) => {
        setGraphState(prev => ({
            ...prev,
            nodes: prev.nodes.map(node =>
                node.id === nodeId ? { ...node, data } as PipelineNode : node
            ),
        }));
        setIsDirty(true);
    }, []);

    const loadFromApi = useCallback((pipeline: Pipeline) => {
        const newState = apiToGraph(pipeline);
        setGraphState(newState);
        setOriginalState(newState);
        setIsDirty(false);
    }, []);

    const toApi = useCallback((pipelineId: string): Pipeline => {
        return graphToApi(pipelineId, graphState.nodes, graphState.edges);
    }, [graphState]);

    const reset = useCallback(() => {
        if (originalState) {
            setGraphState(originalState);
            setIsDirty(false);
        } else {
            setGraphState(createEmptyGraph());
            setIsDirty(false);
        }
    }, [originalState]);

    const markClean = useCallback(() => {
        setOriginalState(graphState);
        setIsDirty(false);
    }, [graphState]);

    return {
        nodes: graphState.nodes,
        edges: graphState.edges,
        isValid: validation.isValid,
        validationErrors: validation.errors,
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
