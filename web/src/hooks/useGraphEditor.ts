import { useState, useCallback } from 'react';
import type { Node, Edge } from '@xyflow/react';

export interface GraphState<N extends Node, E extends Edge> {
    nodes: N[];
    edges: E[];
}

export interface UseGraphEditorOptions<N extends Node, E extends Edge> {
    /** Initial graph state */
    initialState?: GraphState<N, E>;
    /** Function to create empty graph state */
    createEmptyState: () => GraphState<N, E>;
    /** Validation function returning errors array */
    validate?: (nodes: N[], edges: E[]) => string[];
}

export interface UseGraphEditorResult<N extends Node, E extends Edge> {
    /** Current nodes */
    nodes: N[];
    /** Current edges */
    edges: E[];
    /** Whether the graph is valid */
    isValid: boolean;
    /** Validation error messages */
    validationErrors: string[];
    /** Whether there are unsaved changes */
    isDirty: boolean;
    /** Set nodes (marks dirty) */
    setNodes: (nodes: N[]) => void;
    /** Set edges (marks dirty) */
    setEdges: (edges: E[]) => void;
    /** Set nodes without marking dirty (for position changes) */
    setNodesWithoutDirty: (nodes: N[]) => void;
    /** Set edges without marking dirty (for selection changes) */
    setEdgesWithoutDirty: (edges: E[]) => void;
    /** Update a single node's data */
    updateNode: (nodeId: string, data: Record<string, unknown>) => void;
    /** Load state from external source */
    loadState: (state: GraphState<N, E>) => void;
    /** Reset to original state */
    reset: () => void;
    /** Mark current state as clean (after save) */
    markClean: () => void;
}

/**
 * Base hook for managing graph editor state with dirty tracking and validation
 */
export const useGraphEditor = <N extends Node, E extends Edge>({
    initialState,
    createEmptyState,
    validate,
}: UseGraphEditorOptions<N, E>): UseGraphEditorResult<N, E> => {
    const [graphState, setGraphState] = useState<GraphState<N, E>>(() => {
        return initialState ?? createEmptyState();
    });
    const [isDirty, setIsDirty] = useState(false);
    const [originalState, setOriginalState] = useState<GraphState<N, E> | null>(null);

    // Validation
    const validationErrors = validate
        ? validate(graphState.nodes, graphState.edges)
        : [];
    const isValid = validationErrors.length === 0;

    const setNodes = useCallback((nodes: N[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
        setIsDirty(true);
    }, []);

    const setNodesWithoutDirty = useCallback((nodes: N[]) => {
        setGraphState(prev => ({ ...prev, nodes }));
    }, []);

    const setEdges = useCallback((edges: E[]) => {
        setGraphState(prev => ({ ...prev, edges }));
        setIsDirty(true);
    }, []);

    const setEdgesWithoutDirty = useCallback((edges: E[]) => {
        setGraphState(prev => ({ ...prev, edges }));
    }, []);

    const updateNode = useCallback((nodeId: string, data: Record<string, unknown>) => {
        setGraphState(prev => ({
            ...prev,
            nodes: prev.nodes.map(node =>
                node.id === nodeId ? { ...node, data } as N : node
            ),
        }));
        setIsDirty(true);
    }, []);

    const loadState = useCallback((state: GraphState<N, E>) => {
        setGraphState(state);
        setOriginalState(state);
        setIsDirty(false);
    }, []);

    const reset = useCallback(() => {
        if (originalState) {
            setGraphState(originalState);
            setIsDirty(false);
        } else {
            setGraphState(createEmptyState());
            setIsDirty(false);
        }
    }, [originalState, createEmptyState]);

    const markClean = useCallback(() => {
        setOriginalState(graphState);
        setIsDirty(false);
    }, [graphState]);

    return {
        nodes: graphState.nodes,
        edges: graphState.edges,
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
    };
};

