import type { Node, Edge } from '@xyflow/react';

// Node types
export const NODE_TYPE_INPUT = 'input' as const;
export const NODE_TYPE_OUTPUT = 'output' as const;
export const NODE_TYPE_MODULE = 'module' as const;

export const INPUT_NODE_ID = 'input';
export const OUTPUT_NODE_ID = 'output';

// Node data types with index signature for ReactFlow compatibility
export interface InputNodeData extends Record<string, unknown> {
    label: string;
}

export interface OutputNodeData extends Record<string, unknown> {
    label: string;
}

export interface ModuleNodeData extends Record<string, unknown> {
    type: string;
    name: string;
}

// Custom node types
export type InputNode = Node<InputNodeData, typeof NODE_TYPE_INPUT>;
export type OutputNode = Node<OutputNodeData, typeof NODE_TYPE_OUTPUT>;
export type ModuleNode = Node<ModuleNodeData, typeof NODE_TYPE_MODULE>;

export type FunctionNode = InputNode | OutputNode | ModuleNode;

// Edge with weight (only edges from input have weights)
export interface WeightedEdgeData extends Record<string, unknown> {
    weight?: string | number;
    chainName?: string;
}

export type FunctionEdge = Edge<WeightedEdgeData>;

// Graph state
export interface FunctionGraphState {
    nodes: FunctionNode[];
    edges: FunctionEdge[];
}

// Validation result
export interface ValidationResult {
    isValid: boolean;
    errors: string[];
}

// Chain path representation for conversion
export interface ChainPath {
    chainName: string;
    weight: string | number;
    moduleIds: string[]; // node IDs in order
}
