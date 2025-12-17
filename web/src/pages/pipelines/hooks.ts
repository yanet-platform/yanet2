import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Pipeline, PipelineId } from '../../api/pipelines';
import type { FunctionId } from '../../api/common';
import { toaster } from '../../utils';
import { useGraphEditor } from '../../hooks';
import type { PipelineNode, PipelineEdge } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateLinkedList } from './utils';

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
