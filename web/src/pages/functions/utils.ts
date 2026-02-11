import type { Function as APIFunction, FunctionChain, ModuleId } from '../../api/functions';
import type {
    FunctionNode,
    FunctionEdge,
    FunctionGraphState,
    ValidationResult,
    ChainPath,
    ModuleNodeData,
} from './types';
import {
    NODE_TYPE_INPUT,
    NODE_TYPE_OUTPUT,
    NODE_TYPE_MODULE,
    INPUT_NODE_ID,
    OUTPUT_NODE_ID,
} from './types';

// ============================================================================
// Graph Validation
// ============================================================================

/**
 * Check if the graph has cycles using DFS
 */
export const hasNoCycles = (nodes: FunctionNode[], edges: FunctionEdge[]): boolean => {
    const adjacency = new Map<string, string[]>();

    // Build adjacency list
    for (const node of nodes) {
        adjacency.set(node.id, []);
    }
    for (const edge of edges) {
        const neighbors = adjacency.get(edge.source);
        if (neighbors) {
            neighbors.push(edge.target);
        }
    }

    const visited = new Set<string>();
    const recursionStack = new Set<string>();

    const hasCycleDFS = (nodeId: string): boolean => {
        visited.add(nodeId);
        recursionStack.add(nodeId);

        const neighbors = adjacency.get(nodeId) || [];
        for (const neighbor of neighbors) {
            if (!visited.has(neighbor)) {
                if (hasCycleDFS(neighbor)) {
                    return true;
                }
            } else if (recursionStack.has(neighbor)) {
                return true;
            }
        }

        recursionStack.delete(nodeId);
        return false;
    };

    for (const node of nodes) {
        if (!visited.has(node.id)) {
            if (hasCycleDFS(node.id)) {
                return false;
            }
        }
    }

    return true;
};

/**
 * Check if the graph is valid:
 * - All module nodes must be reachable from INPUT
 * - All module nodes must be able to reach OUTPUT
 * - All paths from INPUT must reach OUTPUT
 */
export const allPathsReachOutput = (nodes: FunctionNode[], edges: FunctionEdge[]): boolean => {
    const adjacency = new Map<string, string[]>();
    const reverseAdjacency = new Map<string, string[]>();

    // Build adjacency lists (forward and reverse)
    for (const node of nodes) {
        adjacency.set(node.id, []);
        reverseAdjacency.set(node.id, []);
    }
    for (const edge of edges) {
        const neighbors = adjacency.get(edge.source);
        if (neighbors) {
            neighbors.push(edge.target);
        }
        const reverseNeighbors = reverseAdjacency.get(edge.target);
        if (reverseNeighbors) {
            reverseNeighbors.push(edge.source);
        }
    }

    // Get all module nodes (not INPUT or OUTPUT)
    const moduleNodes = nodes.filter(n =>
        n.id !== INPUT_NODE_ID && n.id !== OUTPUT_NODE_ID
    );

    // Check if there are any edges from input
    const inputNeighbors = adjacency.get(INPUT_NODE_ID) || [];
    if (inputNeighbors.length === 0) {
        // There are nodes but no edges from input - invalid
        return false;
    }

    // Find all nodes reachable from INPUT
    const reachableFromInput = new Set<string>();
    const dfsFromInput = (nodeId: string): void => {
        if (reachableFromInput.has(nodeId)) return;
        reachableFromInput.add(nodeId);
        const neighbors = adjacency.get(nodeId) || [];
        for (const neighbor of neighbors) {
            dfsFromInput(neighbor);
        }
    };
    dfsFromInput(INPUT_NODE_ID);

    // Find all nodes that can reach OUTPUT (reverse DFS)
    const canReachOutput = new Set<string>();
    const dfsToOutput = (nodeId: string): void => {
        if (canReachOutput.has(nodeId)) return;
        canReachOutput.add(nodeId);
        const neighbors = reverseAdjacency.get(nodeId) || [];
        for (const neighbor of neighbors) {
            dfsToOutput(neighbor);
        }
    };
    dfsToOutput(OUTPUT_NODE_ID);

    // All module nodes must be reachable from INPUT AND able to reach OUTPUT
    for (const moduleNode of moduleNodes) {
        if (!reachableFromInput.has(moduleNode.id)) {
            return false; // Module not reachable from INPUT
        }
        if (!canReachOutput.has(moduleNode.id)) {
            return false; // Module cannot reach OUTPUT
        }
    }

    // Check that all paths from INPUT reach OUTPUT (no dead ends)
    const checkAllPathsReachOutput = (nodeId: string, visited: Set<string>): boolean => {
        if (nodeId === OUTPUT_NODE_ID) {
            return true;
        }

        if (visited.has(nodeId)) {
            return false; // Cycle
        }

        visited.add(nodeId);
        const neighbors = adjacency.get(nodeId) || [];

        if (neighbors.length === 0 && nodeId !== OUTPUT_NODE_ID) {
            return false; // Dead end
        }

        for (const neighbor of neighbors) {
            if (!checkAllPathsReachOutput(neighbor, new Set(visited))) {
                return false;
            }
        }

        return true;
    };

    return checkAllPathsReachOutput(INPUT_NODE_ID, new Set());
};

/**
 * Validate the entire graph
 */
export const validateGraph = (nodes: FunctionNode[], edges: FunctionEdge[]): ValidationResult => {
    const errors: string[] = [];

    if (!hasNoCycles(nodes, edges)) {
        errors.push('Graph contains cycles');
    }

    if (!allPathsReachOutput(nodes, edges)) {
        errors.push('Not all paths from input reach output');
    }

    // Validate weights and chain names on edges originating from input
    const inputEdges = edges.filter(edge => edge.source === INPUT_NODE_ID);
    inputEdges.forEach((edge, index) => {
        const chainName = edge.data?.chainName;
        const weight = edge.data?.weight;

        if (!chainName || String(chainName).trim() === '') {
            errors.push(`Chain ${index + 1} is missing a name`);
        }

        const weightStr = weight === undefined || weight === null ? '' : String(weight).trim();
        if (weightStr === '') {
            errors.push(`Chain ${chainName || index + 1} is missing weight`);
        } else if (!/^\d+$/.test(weightStr)) {
            errors.push(`Chain ${chainName || index + 1} has non-numeric weight`);
        }
    });

    return {
        isValid: errors.length === 0,
        errors,
    };
};

// ============================================================================
// API to Graph Conversion
// ============================================================================

/**
 * Convert API Function to graph nodes and edges
 */
export const apiToGraph = (func: APIFunction): FunctionGraphState => {
    const nodes: FunctionNode[] = [];
    const edges: FunctionEdge[] = [];
    const moduleNodeMap = new Map<string, string>(); // key: "chainIdx-moduleIdx", value: nodeId

    // Add input and output nodes with placeholder positions (will be layouted)
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

    const chains = func.chains || [];

    chains.forEach((chain, chainIdx) => {
        const modules = chain.chain?.modules || [];

        // Create module nodes for this chain with placeholder positions
        modules.forEach((module, moduleIdx) => {
            const nodeId = `module-${chainIdx}-${moduleIdx}`;
            moduleNodeMap.set(`${chainIdx}-${moduleIdx}`, nodeId);

            nodes.push({
                id: nodeId,
                type: NODE_TYPE_MODULE,
                position: { x: 0, y: 0 },
                data: {
                    type: module.type || '',
                    name: module.name || '',
                },
            });
        });

        // Create edges for this chain
        if (modules.length === 0) {
            // Direct connection from input to output
            edges.push({
                id: `edge-input-output-${chainIdx}`,
                source: INPUT_NODE_ID,
                target: OUTPUT_NODE_ID,
                data: {
                    weight: chain.weight ?? 0,
                    chainName: chain.chain?.name,
                },
            });
        } else {
            // Input to first module
            edges.push({
                id: `edge-input-${chainIdx}-0`,
                source: INPUT_NODE_ID,
                target: moduleNodeMap.get(`${chainIdx}-0`)!,
                data: {
                    weight: chain.weight ?? 0,
                    chainName: chain.chain?.name,
                },
            });

            // Module to module connections
            for (let i = 0; i < modules.length - 1; i++) {
                edges.push({
                    id: `edge-${chainIdx}-${i}-${i + 1}`,
                    source: moduleNodeMap.get(`${chainIdx}-${i}`)!,
                    target: moduleNodeMap.get(`${chainIdx}-${i + 1}`)!,
                });
            }

            // Last module to output
            edges.push({
                id: `edge-${chainIdx}-${modules.length - 1}-output`,
                source: moduleNodeMap.get(`${chainIdx}-${modules.length - 1}`)!,
                target: OUTPUT_NODE_ID,
            });
        }
    });

    // Apply topological layout to get proper positions
    const layoutedNodes = layoutGraphTopologically(nodes, edges);

    return { nodes: layoutedNodes, edges };
};

// ============================================================================
// Graph to API Conversion
// ============================================================================

/**
 * Find all paths from input to output and extract chain information
 */
const findAllPaths = (nodes: FunctionNode[], edges: FunctionEdge[]): ChainPath[] => {
    const adjacency = new Map<string, { target: string; edge: FunctionEdge }[]>();

    // Build adjacency list with edge info
    for (const node of nodes) {
        adjacency.set(node.id, []);
    }
    for (const edge of edges) {
        const neighbors = adjacency.get(edge.source);
        if (neighbors) {
            neighbors.push({ target: edge.target, edge });
        }
    }

    const paths: ChainPath[] = [];

    const dfs = (
        nodeId: string,
        currentPath: string[],
        weight: string | number | undefined,
        chainName: string | undefined
    ): void => {
        if (nodeId === OUTPUT_NODE_ID) {
            // Found a complete path
            paths.push({
                chainName: chainName || `chain-${paths.length}`,
                weight: weight ?? '1',
                moduleIds: currentPath.filter(id => id !== INPUT_NODE_ID && id !== OUTPUT_NODE_ID),
            });
            return;
        }

        const neighbors = adjacency.get(nodeId) || [];
        for (const { target, edge } of neighbors) {
            // For edges from input, use their weight and chainName
            const edgeWeight = nodeId === INPUT_NODE_ID ? edge.data?.weight : weight;
            const edgeChainName = nodeId === INPUT_NODE_ID ? edge.data?.chainName : chainName;

            dfs(target, [...currentPath, target], edgeWeight, edgeChainName);
        }
    };

    dfs(INPUT_NODE_ID, [INPUT_NODE_ID], undefined, undefined);

    return paths;
};

/**
 * Convert graph nodes and edges to API Function
 */
export const graphToApi = (
    functionId: string,
    nodes: FunctionNode[],
    edges: FunctionEdge[]
): APIFunction => {
    const nodeMap = new Map<string, FunctionNode>();
    for (const node of nodes) {
        nodeMap.set(node.id, node);
    }

    const paths = findAllPaths(nodes, edges);

    const chains: FunctionChain[] = paths.map(path => {
        const modules: ModuleId[] = path.moduleIds.map(nodeId => {
            const node = nodeMap.get(nodeId);
            if (node && node.type === NODE_TYPE_MODULE) {
                const data = node.data as ModuleNodeData;
                return {
                    type: data.type,
                    name: data.name,
                };
            }
            return { type: '', name: '' };
        });

        return {
            chain: {
                name: path.chainName,
                modules,
            },
            weight: parseInt(String(path.weight), 10),
        };
    });

    return {
        id: { name: functionId },
        chains,
    };
};

// ============================================================================
// Helper Functions
// ============================================================================

/**
 * Generate a unique node ID
 */
export const generateNodeId = (): string => {
    return `module-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
};

/**
 * Create initial graph state for a new function
 * Includes a default INPUT -> OUTPUT connection
 */
export const createEmptyGraph = (): FunctionGraphState => {
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
                position: { x: 1000, y: 200 },
                data: { label: 'Output' },
            },
        ],
        edges: [
            {
                id: 'edge-input-output-default',
                source: INPUT_NODE_ID,
                target: OUTPUT_NODE_ID,
                data: {
                    weight: '1',
                    chainName: 'default',
                },
            },
        ],
    };
};

/**
 * Get edges originating from the input node
 */
export const getInputEdges = (edges: FunctionEdge[]): FunctionEdge[] => {
    return edges.filter(edge => edge.source === INPUT_NODE_ID);
};

/**
 * Update weight for an edge from input
 */
export const updateEdgeWeight = (
    edges: FunctionEdge[],
    edgeId: string,
    weight: string | number
): FunctionEdge[] => {
    return edges.map(edge => {
        if (edge.id === edgeId) {
            return {
                ...edge,
                data: {
                    ...edge.data,
                    weight,
                },
            };
        }
        return edge;
    });
};

// ============================================================================
// Graph Layout
// ============================================================================

// Node height constants for vertical centering
const NODE_HEIGHT_INPUT = 70;
const NODE_HEIGHT_OUTPUT = 70;
const NODE_HEIGHT_MODULE = 120;

/**
 * Find all complete chains (paths from input to output) and assign chain indices to nodes.
 * Each node is assigned to the chain it belongs to. Nodes that belong to multiple chains
 * get assigned to the first chain they appear in.
 * Unconnected nodes get their own separate chain indices.
 */
const findChainAssignments = (
    nodes: FunctionNode[],
    edges: FunctionEdge[]
): Map<string, number> => {
    const adjacency = new Map<string, string[]>();

    for (const node of nodes) {
        adjacency.set(node.id, []);
    }
    for (const edge of edges) {
        const neighbors = adjacency.get(edge.source);
        if (neighbors) {
            neighbors.push(edge.target);
        }
    }

    // Find all complete paths from INPUT to OUTPUT
    const allPaths: string[][] = [];

    const findPaths = (nodeId: string, currentPath: string[]): void => {
        if (nodeId === OUTPUT_NODE_ID) {
            allPaths.push([...currentPath]);
            return;
        }

        const neighbors = adjacency.get(nodeId) || [];
        for (const neighbor of neighbors) {
            findPaths(neighbor, [...currentPath, neighbor]);
        }
    };

    findPaths(INPUT_NODE_ID, []);

    // Assign each node to a chain based on the path it appears in
    const nodeToChain = new Map<string, number>();

    allPaths.forEach((path, chainIndex) => {
        for (const nodeId of path) {
            if (nodeId !== OUTPUT_NODE_ID && !nodeToChain.has(nodeId)) {
                nodeToChain.set(nodeId, chainIndex);
            }
        }
    });

    // Assign unconnected module nodes to separate chains
    let nextChainIndex = allPaths.length;
    for (const node of nodes) {
        // Any node that's not INPUT or OUTPUT is a module node
        if (node.id !== INPUT_NODE_ID && node.id !== OUTPUT_NODE_ID && !nodeToChain.has(node.id)) {
            nodeToChain.set(node.id, nextChainIndex++);
        }
    }

    return nodeToChain;
};

/**
 * Perform topological sort and layout the graph
 * Nodes in the same chain are placed on the same Y coordinate
 * INPUT and OUTPUT are centered vertically
 * Unconnected nodes are placed on separate rows
 */
export const layoutGraphTopologically = (
    nodes: FunctionNode[],
    edges: FunctionEdge[]
): FunctionNode[] => {
    // Build adjacency list and in-degree map
    const adjacency = new Map<string, string[]>();
    const inDegree = new Map<string, number>();

    for (const node of nodes) {
        adjacency.set(node.id, []);
        inDegree.set(node.id, 0);
    }

    for (const edge of edges) {
        const neighbors = adjacency.get(edge.source);
        if (neighbors) {
            neighbors.push(edge.target);
        }
        inDegree.set(edge.target, (inDegree.get(edge.target) || 0) + 1);
    }

    // Kahn's algorithm for topological sort with level assignment
    const levels = new Map<string, number>();
    const queue: string[] = [];

    // Start with nodes that have no incoming edges (should be input)
    for (const node of nodes) {
        if (inDegree.get(node.id) === 0) {
            queue.push(node.id);
            levels.set(node.id, 0);
        }
    }

    while (queue.length > 0) {
        const nodeId = queue.shift()!;
        const currentLevel = levels.get(nodeId) || 0;
        const neighbors = adjacency.get(nodeId) || [];

        for (const neighbor of neighbors) {
            const newLevel = currentLevel + 1;
            const existingLevel = levels.get(neighbor);

            // Assign the maximum level (longest path)
            if (existingLevel === undefined || newLevel > existingLevel) {
                levels.set(neighbor, newLevel);
            }

            const newInDegree = (inDegree.get(neighbor) || 0) - 1;
            inDegree.set(neighbor, newInDegree);

            if (newInDegree === 0) {
                queue.push(neighbor);
            }
        }
    }

    // Find chain assignments for nodes
    const nodeToChain = findChainAssignments(nodes, edges);

    // Get sorted unique chain indices
    const uniqueChainIndices = [...new Set(nodeToChain.values())].sort((a, b) => a - b);
    const chainCount = Math.max(uniqueChainIndices.length, 1);

    // Find max level for connected nodes
    let maxLevel = 1; // At least 1 to have space between INPUT and OUTPUT
    for (const node of nodes) {
        if (levels.has(node.id)) {
            const level = levels.get(node.id) ?? 0;
            maxLevel = Math.max(maxLevel, level);
        }
    }

    // Ensure OUTPUT is at least at level 2 if there are no connected nodes
    if (maxLevel === 0) {
        maxLevel = 2;
    }

    // Layout constants
    const horizontalSpacing = 300;
    const verticalSpacing = 150;
    const startX = 100;
    const centerY = 200;

    // Calculate Y positions for chains - map chain index to Y position
    const chainYPositions = new Map<number, number>();
    const startY = centerY - ((chainCount - 1) * verticalSpacing) / 2;
    uniqueChainIndices.forEach((chainIdx, i) => {
        chainYPositions.set(chainIdx, startY + i * verticalSpacing);
    });

    // Calculate positions
    const positions = new Map<string, { x: number; y: number }>();

    // Position INPUT at level 0, centered vertically
    positions.set(INPUT_NODE_ID, {
        x: startX,
        y: centerY - NODE_HEIGHT_INPUT / 2,
    });

    // Position OUTPUT at max level, centered vertically
    positions.set(OUTPUT_NODE_ID, {
        x: startX + maxLevel * horizontalSpacing,
        y: centerY - NODE_HEIGHT_OUTPUT / 2,
    });

    // Position module nodes based on their chain and level
    for (const node of nodes) {
        if (node.id === INPUT_NODE_ID || node.id === OUTPUT_NODE_ID) {
            continue;
        }

        // For unconnected nodes, place them at level 1 (middle)
        const level = levels.get(node.id) ?? 1;
        const chain = nodeToChain.get(node.id) ?? 0;
        const chainY = chainYPositions.get(chain) ?? centerY;

        positions.set(node.id, {
            x: startX + level * horizontalSpacing,
            y: chainY - NODE_HEIGHT_MODULE / 2,
        });
    }

    // Return nodes with updated positions
    return nodes.map(node => ({
        ...node,
        position: positions.get(node.id) || node.position,
    }));
};
