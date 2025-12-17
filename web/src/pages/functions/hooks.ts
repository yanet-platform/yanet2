import { useState, useCallback, useEffect } from 'react';
import { API } from '../../api';
import type { Function as APIFunction } from '../../api/functions';
import type { FunctionId } from '../../api/common';
import { toaster } from '../../utils';
import { useGraphEditor } from '../../hooks';
import type { FunctionNode, FunctionEdge } from './types';
import { apiToGraph, graphToApi, createEmptyGraph, validateGraph } from './utils';

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
