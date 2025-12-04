import { useCallback } from 'react';
import type { Graph, TBlockId, TConnection } from '@gravity-ui/graph';

export interface ConnectionValidation {
    valid: boolean;
    message?: string;
}

/**
 * Hook for managing pipeline graph connections and validation.
 * Unlike functions graph, pipeline is strictly linear (single chain).
 */
export const usePipelineConnections = (graph: Graph) => {
    const getConnections = useCallback((): TConnection[] => {
        return graph.rootStore.connectionsList.getConnections().map(state => state.toJSON());
    }, [graph]);

    const getOutgoingConnections = useCallback((blockId: TBlockId): TConnection[] => {
        return getConnections().filter(conn => conn.sourceBlockId === blockId);
    }, [getConnections]);

    const getIncomingConnections = useCallback((blockId: TBlockId): TConnection[] => {
        return getConnections().filter(conn => conn.targetBlockId === blockId);
    }, [getConnections]);

    const hasOutgoingConnection = useCallback((blockId: TBlockId): boolean => {
        return getOutgoingConnections(blockId).length > 0;
    }, [getOutgoingConnections]);

    const hasIncomingConnection = useCallback((blockId: TBlockId): boolean => {
        return getIncomingConnections(blockId).length > 0;
    }, [getIncomingConnections]);

    const wouldCreateCycle = useCallback((sourceId: TBlockId, targetId: TBlockId): boolean => {
        // Self-connection = cycle
        if (sourceId === targetId) {
            return true;
        }

        // Build adjacency map
        const connections = getConnections();
        const outgoing = new Map<TBlockId, Set<TBlockId>>();
        connections.forEach(conn => {
            if (conn.sourceBlockId && conn.targetBlockId) {
                if (!outgoing.has(conn.sourceBlockId)) {
                    outgoing.set(conn.sourceBlockId, new Set());
                }
                outgoing.get(conn.sourceBlockId)!.add(conn.targetBlockId);
            }
        });

        // Add proposed connection
        if (!outgoing.has(sourceId)) {
            outgoing.set(sourceId, new Set());
        }
        outgoing.get(sourceId)!.add(targetId);

        // BFS to check if we can reach sourceId starting from targetId
        const visited = new Set<TBlockId>();
        const queue: TBlockId[] = [targetId];

        while (queue.length > 0) {
            const current = queue.shift()!;
            if (current === sourceId) {
                return true; // Found cycle
            }
            if (visited.has(current)) continue;
            visited.add(current);

            const targets = outgoing.get(current);
            if (targets) {
                targets.forEach(t => {
                    if (!visited.has(t)) {
                        queue.push(t);
                    }
                });
            }
        }

        return false;
    }, [getConnections]);

    const connectionExists = useCallback((sourceBlockId: TBlockId, targetBlockId: TBlockId): boolean => {
        return getConnections().some(
            conn => conn.sourceBlockId === sourceBlockId && conn.targetBlockId === targetBlockId
        );
    }, [getConnections]);

    const validateConnection = useCallback((
        sourceBlockId: TBlockId,
        targetBlockId: TBlockId
    ): ConnectionValidation => {
        // Rule 1: No self-connections
        if (sourceBlockId === targetBlockId) {
            return { valid: false, message: 'Cannot connect block to itself' };
        }

        // Rule 2: No duplicate connections
        if (connectionExists(sourceBlockId, targetBlockId)) {
            return { valid: false, message: 'Connection already exists' };
        }

        // Rule 3: All blocks can have only 1 outgoing connection (including INPUT - linear pipeline)
        if (hasOutgoingConnection(sourceBlockId)) {
            return { valid: false, message: 'Output already has a connection' };
        }

        // Rule 4: All blocks can have only 1 incoming connection (including OUTPUT - linear pipeline)
        if (hasIncomingConnection(targetBlockId)) {
            return { valid: false, message: 'Input already has a connection' };
        }

        // Rule 5: No cycles
        if (wouldCreateCycle(sourceBlockId, targetBlockId)) {
            return { valid: false, message: 'Connection would create a cycle' };
        }

        return { valid: true };
    }, [connectionExists, hasOutgoingConnection, hasIncomingConnection, wouldCreateCycle]);

    return {
        getConnections,
        getOutgoingConnections,
        getIncomingConnections,
        hasOutgoingConnection,
        hasIncomingConnection,
        wouldCreateCycle,
        connectionExists,
        validateConnection,
    };
};
