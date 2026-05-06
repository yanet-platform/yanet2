import type { Node, Edge } from '@xyflow/react';

// Node types
export const NODE_TYPE_INPUT = 'input' as const;
export const NODE_TYPE_OUTPUT = 'output' as const;
export const NODE_TYPE_FUNCTION_REF = 'functionRef' as const;

export const INPUT_NODE_ID = 'input';
export const OUTPUT_NODE_ID = 'output';

// Node data types with index signature for ReactFlow compatibility
export interface InputNodeData extends Record<string, unknown> {
    label: string;
}

export interface OutputNodeData extends Record<string, unknown> {
    label: string;
}

export interface FunctionRefNodeData extends Record<string, unknown> {
    functionName: string;
}

// Custom node types
export type InputNode = Node<InputNodeData, typeof NODE_TYPE_INPUT>;
export type OutputNode = Node<OutputNodeData, typeof NODE_TYPE_OUTPUT>;
export type FunctionRefNode = Node<FunctionRefNodeData, typeof NODE_TYPE_FUNCTION_REF>;

export type PipelineNode = InputNode | OutputNode | FunctionRefNode;

// Edge without weight data (simple linked-list)
export type PipelineEdge = Edge<Record<string, unknown>>;

// Graph state
export interface PipelineGraphState {
    nodes: PipelineNode[];
    edges: PipelineEdge[];
}

// Validation result
export interface ValidationResult {
    isValid: boolean;
    errors: string[];
}
