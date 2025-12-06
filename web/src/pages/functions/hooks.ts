import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Function as APIFunction } from '../../api/functions';
import type { FunctionId } from '../../api/common';
import type { InspectResponse } from '../../api/inspect';
import { toaster } from '../../utils';
import type { FunctionNode, FunctionEdge, FunctionGraphState } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateGraph } from './utils';

export type FunctionMapByInstance = Record<number, Record<string, APIFunction>>;

export interface InstanceData {
    instance: number;
    functionIds: FunctionId[];
}

export interface UseFunctionDataResult {
    instances: InstanceData[];
    loading: boolean;
    functionsLoading: boolean;
    functionsByInstance: FunctionMapByInstance;
    error: string | null;
    reloadInstances: () => Promise<void>;
    loadFunction: (instance: number, functionId: FunctionId) => Promise<APIFunction | null>;
    createFunction: (instance: number, name: string) => Promise<boolean>;
    updateFunction: (instance: number, func: APIFunction) => Promise<boolean>;
    deleteFunction: (instance: number, functionId: FunctionId) => Promise<boolean>;
}

/**
 * Hook for managing function data and API interactions
 */
export const useFunctionData = (): UseFunctionDataResult => {
    const [instances, setInstances] = useState<InstanceData[]>([]);
    const [instancesLoading, setInstancesLoading] = useState(true);
    const [functionsLoading, setFunctionsLoading] = useState(true);
    const [functionsByInstance, setFunctionsByInstance] = useState<FunctionMapByInstance>({});
    const [error, setError] = useState<string | null>(null);

    const loadInstancesAndFunctions = useCallback(async (): Promise<void> => {
        setInstancesLoading(true);
        setFunctionsLoading(true);
        setError(null);

        try {
            // First get all instances from inspect
            const inspectResponse: InspectResponse = await API.inspect.inspect();
            const instanceIndices = inspectResponse.instanceIndices || [];

            // Then load function list for each instance
            const instanceDataPromises = instanceIndices.map(async (instanceIdx) => {
                try {
                    const response = await API.functions.list({ instance: instanceIdx });
                    return {
                        instance: instanceIdx,
                        functionIds: response.ids || [],
                    };
                } catch (err) {
                    console.error(`Failed to load functions for instance ${instanceIdx}:`, err);
                    return {
                        instance: instanceIdx,
                        functionIds: [],
                    };
                }
            });

            const instanceData = await Promise.all(instanceDataPromises);
            setInstances(instanceData);

            // Preload functions for all instances to avoid per-card loading flashes
            const functionsPerInstance = await Promise.all(instanceData.map(async ({ instance, functionIds }) => {
                if (functionIds.length === 0) {
                    return { instance, functions: {} as Record<string, APIFunction> };
                }

                const functions: Record<string, APIFunction> = {};

                await Promise.all(functionIds.map(async (functionId) => {
                    try {
                        const response = await API.functions.get({ instance, id: functionId });
                        const func = response.function;
                        const key = func?.id?.name || functionId.name;
                        if (func && key) {
                            functions[key] = func;
                        }
                    } catch (err) {
                        toaster.error('function-get-error', `Failed to load function ${functionId.name}`, err);
                    }
                }));

                return { instance, functions };
            }));

            const nextFunctionsByInstance: FunctionMapByInstance = {};
            functionsPerInstance.forEach(({ instance, functions }) => {
                nextFunctionsByInstance[instance] = functions;
            });
            setFunctionsByInstance(nextFunctionsByInstance);
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to load instances';
            setError(message);
            toaster.error('functions-load-error', 'Failed to load functions', err);
        } finally {
            setInstancesLoading(false);
            setFunctionsLoading(false);
        }
    }, []);

    useEffect(() => {
        loadInstancesAndFunctions();
    }, [loadInstancesAndFunctions]);

    const loadFunction = useCallback(async (
        instance: number,
        functionId: FunctionId
    ): Promise<APIFunction | null> => {
        const cached = functionsByInstance[instance]?.[functionId.name || ''];
        if (cached) {
            return cached;
        }

        try {
            const response = await API.functions.get({ instance, id: functionId });
            const func = response.function || null;

            const funcName = func?.id?.name;
            if (func && funcName) {
                setFunctionsByInstance(prev => ({
                    ...prev,
                    [instance]: {
                        ...(prev[instance] || {}),
                        [funcName]: func,
                    },
                }));
            }

            return func;
        } catch (err) {
            toaster.error('function-get-error', `Failed to load function ${functionId.name}`, err);
            return null;
        }
    }, [functionsByInstance]);

    const createFunction = useCallback(async (
        instance: number,
        name: string
    ): Promise<boolean> => {
        try {
            const newFunction: APIFunction = {
                id: { name },
                chains: [],
            };

            await API.functions.update({ instance, function: newFunction });

            // Reload to get updated list
            await loadInstancesAndFunctions();

            toaster.success('function-create-success', `Function "${name}" created`);
            return true;
        } catch (err) {
            toaster.error('function-create-error', `Failed to create function "${name}"`, err);
            return false;
        }
    }, [loadInstancesAndFunctions]);

    const updateFunction = useCallback(async (
        instance: number,
        func: APIFunction
    ): Promise<boolean> => {
        try {
            await API.functions.update({ instance, function: func });
            toaster.success('function-update-success', `Function "${func.id?.name}" saved`);

            const funcName = func.id?.name;
            if (funcName) {
                setFunctionsByInstance(prev => ({
                    ...prev,
                    [instance]: {
                        ...(prev[instance] || {}),
                        [funcName]: func,
                    },
                }));
            }

            return true;
        } catch (err) {
            toaster.error('function-update-error', `Failed to save function "${func.id?.name}"`, err);
            return false;
        }
    }, []);

    const deleteFunction = useCallback(async (
        instance: number,
        functionId: FunctionId
    ): Promise<boolean> => {
        try {
            await API.functions.delete({ instance, id: functionId });

            // Update local state
            setInstances(prev => prev.map(inst => {
                if (inst.instance === instance) {
                    return {
                        ...inst,
                        functionIds: inst.functionIds.filter(
                            id => id.name !== functionId.name
                        ),
                    };
                }
                return inst;
            }));

            const functionName = functionId.name;
            if (functionName) {
                setFunctionsByInstance(prev => {
                    const instanceFunctions = prev[instance];
                    if (!instanceFunctions) {
                        return prev;
                    }
                    const { [functionName]: _removed, ...rest } = instanceFunctions;
                    return {
                        ...prev,
                        [instance]: rest,
                    };
                });
            }

            toaster.success('function-delete-success', `Function "${functionId.name}" deleted`);
            return true;
        } catch (err) {
            toaster.error('function-delete-error', `Failed to delete function "${functionId.name}"`, err);
            return false;
        }
    }, []);

    const loading = instancesLoading || functionsLoading;

    return {
        instances,
        loading,
        functionsLoading,
        functionsByInstance,
        error,
        reloadInstances: loadInstancesAndFunctions,
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
    const [graphState, setGraphState] = useState<FunctionGraphState>(() => {
        if (initialFunction) {
            return apiToGraph(initialFunction);
        }
        return createEmptyGraph();
    });
    const [isDirty, setIsDirty] = useState(false);
    const [originalState, setOriginalState] = useState<FunctionGraphState | null>(null);

    const validation = validateGraph(graphState.nodes, graphState.edges);

    const setNodes = useCallback((nodes: FunctionNode[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
        setIsDirty(true);
    }, []);

    const setNodesWithoutDirty = useCallback((nodes: FunctionNode[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
    }, []);

    const setEdges = useCallback((edges: FunctionEdge[]) => {
        setGraphState(prev => ({ ...prev, edges }));
        setIsDirty(true);
    }, []);

    const setEdgesWithoutDirty = useCallback((edges: FunctionEdge[]) => {
        setGraphState(prev => ({ ...prev, edges }));
    }, []);

    const updateNode = useCallback((nodeId: string, data: Record<string, unknown>) => {
        setGraphState(prev => ({
            ...prev,
            nodes: prev.nodes.map(node =>
                node.id === nodeId ? { ...node, data } as FunctionNode : node
            ),
        }));
        setIsDirty(true);
    }, []);

    const loadFromApi = useCallback((func: APIFunction) => {
        const newState = apiToGraph(func);
        setGraphState(newState);
        setOriginalState(newState);
        setIsDirty(false);
    }, []);

    const toApi = useCallback((functionId: string): APIFunction => {
        return graphToApi(functionId, graphState.nodes, graphState.edges);
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
