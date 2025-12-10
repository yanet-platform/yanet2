import { useState, useEffect, useCallback, useRef } from 'react';
import type { OnSelectionChangeParams } from '@xyflow/react';
import { useOnSelectionChange } from '@xyflow/react';

export interface UseGraphFocusOptions {
    /** Reference to the wrapper element */
    wrapperRef: React.RefObject<HTMLDivElement>;
    /** Callback when focus changes */
    onFocusChange?: (focused: boolean) => void;
}

export interface UseGraphFocusResult {
    /** Whether the graph is currently focused */
    isFocused: boolean;
    /** Set focus state */
    setIsFocused: (focused: boolean) => void;
    /** Currently selected node IDs */
    selectedNodeIds: string[];
    /** Currently selected edge IDs */
    selectedEdgeIds: string[];
}

/**
 * Hook for managing graph focus and selection state
 */
export const useGraphFocus = ({
    wrapperRef,
    onFocusChange,
}: UseGraphFocusOptions): UseGraphFocusResult => {
    const [isFocused, setIsFocused] = useState(false);
    const [selectedNodeIds, setSelectedNodeIds] = useState<string[]>([]);
    const [selectedEdgeIds, setSelectedEdgeIds] = useState<string[]>([]);

    // Track selection changes
    const onSelectionChange = useCallback(({ nodes, edges }: OnSelectionChangeParams) => {
        setSelectedNodeIds(nodes.map(n => n.id));
        setSelectedEdgeIds(edges.map(e => e.id));
    }, []);

    useOnSelectionChange({ onChange: onSelectionChange });

    // Track focus state
    useEffect(() => {
        const wrapper = wrapperRef.current;
        if (!wrapper) return;

        const handleFocusIn = () => {
            setIsFocused(true);
            onFocusChange?.(true);
        };
        const handleFocusOut = (e: FocusEvent) => {
            // Only lose focus if the new focus target is outside this wrapper
            if (!wrapper.contains(e.relatedTarget as HTMLElement | null)) {
                setIsFocused(false);
                onFocusChange?.(false);
            }
        };
        const handleClick = () => {
            setIsFocused(true);
            onFocusChange?.(true);
        };

        wrapper.addEventListener('focusin', handleFocusIn);
        wrapper.addEventListener('focusout', handleFocusOut);
        wrapper.addEventListener('click', handleClick);

        return () => {
            wrapper.removeEventListener('focusin', handleFocusIn);
            wrapper.removeEventListener('focusout', handleFocusOut);
            wrapper.removeEventListener('click', handleClick);
        };
    }, [wrapperRef, onFocusChange]);

    return {
        isFocused,
        setIsFocused,
        selectedNodeIds,
        selectedEdgeIds,
    };
};

export interface UseGraphKeyboardOptions {
    /** Whether the graph is focused */
    isFocused: boolean;
    /** Currently selected node IDs */
    selectedNodeIds: string[];
    /** Currently selected edge IDs */
    selectedEdgeIds: string[];
    /** Node IDs that cannot be deleted (e.g., input/output) */
    protectedNodeIds: string[];
    /** Callback to delete nodes */
    onDeleteNodes: (nodeIds: string[]) => void;
    /** Callback to delete edges */
    onDeleteEdges: (edgeIds: string[]) => void;
    /** Callback for layout action (S key) */
    onLayout?: () => void;
}

/**
 * Hook for handling graph keyboard shortcuts
 */
export const useGraphKeyboard = ({
    isFocused,
    selectedNodeIds,
    selectedEdgeIds,
    protectedNodeIds,
    onDeleteNodes,
    onDeleteEdges,
    onLayout,
}: UseGraphKeyboardOptions): void => {
    const protectedIdsRef = useRef(protectedNodeIds);
    protectedIdsRef.current = protectedNodeIds;

    // Delete key handler
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isFocused) return;

            // Don't trigger if user is typing in an input
            if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
                return;
            }

            if (e.key === 'Delete' || e.key === 'Backspace') {
                e.preventDefault();
                e.stopPropagation();

                // Delete selected nodes (except protected ones)
                if (selectedNodeIds.length > 0) {
                    const nodesToDelete = selectedNodeIds.filter(
                        id => !protectedIdsRef.current.includes(id)
                    );
                    if (nodesToDelete.length > 0) {
                        onDeleteNodes(nodesToDelete);
                    }
                }

                // Delete selected edges
                if (selectedEdgeIds.length > 0) {
                    onDeleteEdges(selectedEdgeIds);
                }
            }
        };

        document.addEventListener('keydown', handleKeyDown, true);
        return () => document.removeEventListener('keydown', handleKeyDown, true);
    }, [isFocused, selectedNodeIds, selectedEdgeIds, onDeleteNodes, onDeleteEdges]);

    // Layout key handler (S key)
    useEffect(() => {
        if (!onLayout) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isFocused) return;

            if (e.key === 's' || e.key === 'S') {
                // Don't trigger if user is typing in an input
                if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
                    return;
                }
                e.preventDefault();
                onLayout();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [isFocused, onLayout]);
};
