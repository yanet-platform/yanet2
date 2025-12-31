import type { Pipeline, FunctionId } from '../../api/pipelines';
import type {
    PipelineNode,
    PipelineEdge,
    PipelineGraphState,
    ValidationResult,
    FunctionRefNodeData,
} from './types';
import {
    NODE_TYPE_INPUT,
    NODE_TYPE_OUTPUT,
    NODE_TYPE_FUNCTION_REF,
    INPUT_NODE_ID,
    OUTPUT_NODE_ID,
} from './types';

// ============================================================================
// Graph Validation (Linked-List specific)
// ============================================================================

/**
 * Check if the graph is a valid linked-list:
 * - Each node (except INPUT/OUTPUT) has exactly 1 incoming and 1 outgoing edge
 * - INPUT has no incoming edges and exactly 1 outgoing edge
 * - OUTPUT has exactly 1 incoming edge and no outgoing edges
 * - All nodes must be on the path from INPUT to OUTPUT
 */
export const validateLinkedList = (nodes: PipelineNode[], edges: PipelineEdge[]): ValidationResult => {
    const errors: string[] = [];

    // Basic size check: for a single linear chain we expect edges = nodes - 1
    // (includes input/output). If this is not true, the graph cannot be a
    // fully connected linked list.
    if (nodes.length > 0 && edges.length !== nodes.length - 1) {
        errors.push('Graph must form a single chain from input to output');
    }
    
    // Count incoming and outgoing edges for each node
    const incomingCount = new Map<string, number>();
    const outgoingCount = new Map<string, number>();
    
    for (const node of nodes) {
        incomingCount.set(node.id, 0);
        outgoingCount.set(node.id, 0);
    }
    
    for (const edge of edges) {
        incomingCount.set(edge.target, (incomingCount.get(edge.target) || 0) + 1);
        outgoingCount.set(edge.source, (outgoingCount.get(edge.source) || 0) + 1);
    }
    
    // Validate INPUT node
    if ((incomingCount.get(INPUT_NODE_ID) || 0) !== 0) {
        errors.push('Input node should not have incoming edges');
    }
    if ((outgoingCount.get(INPUT_NODE_ID) || 0) !== 1) {
        errors.push('Input node must have exactly 1 outgoing edge');
    }
    
    // Validate OUTPUT node
    if ((outgoingCount.get(OUTPUT_NODE_ID) || 0) !== 0) {
        errors.push('Output node should not have outgoing edges');
    }
    if ((incomingCount.get(OUTPUT_NODE_ID) || 0) !== 1) {
        errors.push('Output node must have exactly 1 incoming edge');
    }
    
    // Validate function ref nodes (each should have exactly 1 in and 1 out)
    for (const node of nodes) {
        if (node.id === INPUT_NODE_ID || node.id === OUTPUT_NODE_ID) continue;
        
        const incoming = incomingCount.get(node.id) || 0;
        const outgoing = outgoingCount.get(node.id) || 0;
        
        if (incoming !== 1) {
            errors.push(`Node "${(node.data as FunctionRefNodeData).functionName || node.id}" must have exactly 1 incoming edge`);
        }
        if (outgoing !== 1) {
            errors.push(`Node "${(node.data as FunctionRefNodeData).functionName || node.id}" must have exactly 1 outgoing edge`);
        }
    }
    
    // Check that all nodes are reachable from INPUT and can reach OUTPUT
    const reachableFromInput = new Set<string>();
    const canReachOutput = new Set<string>();
    
    // Build adjacency lists
    const adjacency = new Map<string, string[]>();
    const reverseAdjacency = new Map<string, string[]>();
    
    for (const node of nodes) {
        adjacency.set(node.id, []);
        reverseAdjacency.set(node.id, []);
    }
    
    for (const edge of edges) {
        adjacency.get(edge.source)?.push(edge.target);
        reverseAdjacency.get(edge.target)?.push(edge.source);
    }
    
    // DFS from INPUT
    const dfsFromInput = (nodeId: string): void => {
        if (reachableFromInput.has(nodeId)) return;
        reachableFromInput.add(nodeId);
        for (const neighbor of adjacency.get(nodeId) || []) {
            dfsFromInput(neighbor);
        }
    };
    dfsFromInput(INPUT_NODE_ID);
    
    // DFS to OUTPUT (reverse)
    const dfsToOutput = (nodeId: string): void => {
        if (canReachOutput.has(nodeId)) return;
        canReachOutput.add(nodeId);
        for (const neighbor of reverseAdjacency.get(nodeId) || []) {
            dfsToOutput(neighbor);
        }
    };
    dfsToOutput(OUTPUT_NODE_ID);
    
    // Check reachability for all nodes and ensure output is reachable
    if (!reachableFromInput.has(OUTPUT_NODE_ID)) {
        errors.push('Output node is not reachable from input');
    }
    for (const node of nodes) {
        if (node.id === INPUT_NODE_ID || node.id === OUTPUT_NODE_ID) continue;
        
        if (!reachableFromInput.has(node.id)) {
            errors.push(`Node "${(node.data as FunctionRefNodeData).functionName || node.id}" is not reachable from input`);
        }
        if (!canReachOutput.has(node.id)) {
            errors.push(`Node "${(node.data as FunctionRefNodeData).functionName || node.id}" cannot reach output`);
        }
    }
    
    return {
        isValid: errors.length === 0,
        errors,
    };
};

// ============================================================================
// API to Graph Conversion
// ============================================================================

/**
 * Convert API Pipeline to graph nodes and edges
 */
export const apiToGraph = (pipeline: Pipeline): PipelineGraphState => {
    const nodes: PipelineNode[] = [];
    const edges: PipelineEdge[] = [];
    
    // Add input and output nodes
    nodes.push({
        id: INPUT_NODE_ID,
        type: NODE_TYPE_INPUT,
        position: { x: 0, y: 0 },
        data: { label: 'Input' },
    });
    
    nodes.push({
        id: OUTPUT_NODE_ID,
        type: NODE_TYPE_OUTPUT,
        position: { x: 0, y: 0 },
        data: { label: 'Output' },
    });
    
    const functions = pipeline.functions || [];
    
    if (functions.length === 0) {
        // Direct connection from input to output
        edges.push({
            id: 'edge-input-output',
            source: INPUT_NODE_ID,
            target: OUTPUT_NODE_ID,
        });
    } else {
        // Create function ref nodes
        functions.forEach((func, idx) => {
            const nodeId = `func-${idx}`;
            nodes.push({
                id: nodeId,
                type: NODE_TYPE_FUNCTION_REF,
                position: { x: 0, y: 0 },
                data: { functionName: func.name || '' },
            });
        });
        
        // Create edges for linked-list
        // Input -> first function
        edges.push({
            id: 'edge-input-0',
            source: INPUT_NODE_ID,
            target: 'func-0',
        });
        
        // Function -> Function connections
        for (let i = 0; i < functions.length - 1; i++) {
            edges.push({
                id: `edge-${i}-${i + 1}`,
                source: `func-${i}`,
                target: `func-${i + 1}`,
            });
        }
        
        // Last function -> Output
        edges.push({
            id: `edge-${functions.length - 1}-output`,
            source: `func-${functions.length - 1}`,
            target: OUTPUT_NODE_ID,
        });
    }
    
    // Apply layout
    const layoutedNodes = layoutLinkedList(nodes, edges);
    
    return { nodes: layoutedNodes, edges };
};

// ============================================================================
// Graph to API Conversion
// ============================================================================

/**
 * Convert graph nodes and edges to API Pipeline
 */
export const graphToApi = (
    pipelineId: string,
    nodes: PipelineNode[],
    edges: PipelineEdge[]
): Pipeline => {
    // Build adjacency list
    const adjacency = new Map<string, string>();
    for (const edge of edges) {
        // In a valid linked list there should be only one outgoing per source
        if (adjacency.has(edge.source)) {
            throw new Error(`Multiple outgoing edges from node "${edge.source}"`);
        }
        adjacency.set(edge.source, edge.target);
    }
    
    // Traverse from INPUT to OUTPUT to get ordered functions
    const functions: FunctionId[] = [];
    let currentId = INPUT_NODE_ID;
    const visited = new Set<string>();
    
    while (currentId !== OUTPUT_NODE_ID) {
        if (visited.has(currentId)) {
            throw new Error('Cycle detected while building pipeline');
        }
        visited.add(currentId);

        const nextId = adjacency.get(currentId);
        if (!nextId) {
            throw new Error(`Pipeline graph is incomplete: no connection from "${currentId}"`);
        }
        
        if (nextId !== OUTPUT_NODE_ID) {
            const node = nodes.find(n => n.id === nextId);
            if (node && node.type === NODE_TYPE_FUNCTION_REF) {
                const data = node.data as FunctionRefNodeData;
                functions.push({ name: data.functionName });
            }
        }
        
        currentId = nextId;
    }
    
    // Ensure all nodes are on the traversed path (no disconnected nodes)
    const expectedNodeCount = nodes.length;
    const traversedCount = visited.size + 1; // +1 for OUTPUT
    if (expectedNodeCount !== traversedCount) {
        throw new Error('Pipeline graph contains disconnected nodes');
    }
    
    return {
        id: { name: pipelineId },
        functions,
    };
};

// ============================================================================
// Helper Functions
// ============================================================================

/**
 * Generate a unique node ID
 */
export const generateNodeId = (): string => {
    return `func-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
};

/**
 * Create initial graph state for a new pipeline
 * Includes a default INPUT -> OUTPUT connection
 */
export const createEmptyGraph = (): PipelineGraphState => {
    return {
        nodes: [
            {
                id: INPUT_NODE_ID,
                type: NODE_TYPE_INPUT,
                position: { x: 100, y: 200 },
                data: { label: 'Input' },
            },
            {
                id: OUTPUT_NODE_ID,
                type: NODE_TYPE_OUTPUT,
                position: { x: 700, y: 200 },
                data: { label: 'Output' },
            },
        ],
        edges: [
            {
                id: 'edge-input-output-default',
                source: INPUT_NODE_ID,
                target: OUTPUT_NODE_ID,
            },
        ],
    };
};

// ============================================================================
// Graph Layout
// ============================================================================

// Node height constants for vertical centering
const NODE_HEIGHT_INPUT = 70;
const NODE_HEIGHT_OUTPUT = 70;
const NODE_HEIGHT_FUNCTION_REF = 110;

/**
 * Get node height based on node type
 */
const getNodeHeight = (nodeId: string): number => {
    if (nodeId === INPUT_NODE_ID) return NODE_HEIGHT_INPUT;
    if (nodeId === OUTPUT_NODE_ID) return NODE_HEIGHT_OUTPUT;
    return NODE_HEIGHT_FUNCTION_REF;
};

/**
 * Layout nodes in a horizontal linked-list fashion
 */
export const layoutLinkedList = (
    nodes: PipelineNode[],
    edges: PipelineEdge[]
): PipelineNode[] => {
    // Build adjacency list
    const adjacency = new Map<string, string>();
    for (const edge of edges) {
        adjacency.set(edge.source, edge.target);
    }
    
    // Calculate positions by traversing from INPUT
    const positions = new Map<string, { x: number; y: number }>();
    const horizontalSpacing = 300;
    const centerY = 200;
    const startX = 100;
    
    let currentId: string | undefined = INPUT_NODE_ID;
    let level = 0;
    
    while (currentId) {
        const nodeHeight = getNodeHeight(currentId);
        positions.set(currentId, {
            x: startX + level * horizontalSpacing,
            y: centerY - nodeHeight / 2,
        });
        
        const nextId = adjacency.get(currentId);
        if (nextId && !positions.has(nextId)) {
            currentId = nextId;
            level++;
        } else {
            break;
        }
    }
    
    // Position any disconnected nodes
    let disconnectedLevel = level + 1;
    for (const node of nodes) {
        if (!positions.has(node.id)) {
            const nodeHeight = getNodeHeight(node.id);
            positions.set(node.id, {
                x: startX + disconnectedLevel * horizontalSpacing,
                y: centerY + 100 - nodeHeight / 2,
            });
            disconnectedLevel++;
        }
    }
    
    return nodes.map(node => ({
        ...node,
        position: positions.get(node.id) || node.position,
    }));
};

