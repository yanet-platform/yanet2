import React, { useCallback, useRef, useMemo, useEffect, useId, useState } from 'react';
import {
    ReactFlow,
    Background,
    useNodesState,
    useEdgesState,
    addEdge,
    BackgroundVariant,
    useReactFlow,
    ReactFlowProvider,
    Panel,
    useOnSelectionChange,
    getBezierPath,
    BaseEdge,
} from '@xyflow/react';
import type {
    Connection,
    OnConnect,
    OnConnectEnd,
    OnConnectStart,
    NodeTypes,
    EdgeTypes,
    Node,
    Edge,
    EdgeProps,
    OnSelectionChangeParams,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import './nodes/nodes.css';
import { Button } from '@gravity-ui/uikit';
import { LayoutCells } from '@gravity-ui/icons';

import { InputNode, OutputNode, FunctionRefNode, CounterEdge } from './nodes';
import type { PipelineNode, PipelineEdge, FunctionRefNodeData } from './types';
import {
    NODE_TYPE_INPUT,
    NODE_TYPE_OUTPUT,
    NODE_TYPE_FUNCTION_REF,
    INPUT_NODE_ID,
    OUTPUT_NODE_ID,
} from './types';
import { generateNodeId, layoutLinkedList } from './utils';

const nodeTypes: NodeTypes = {
    [NODE_TYPE_INPUT]: InputNode as NodeTypes[string],
    [NODE_TYPE_OUTPUT]: OutputNode as NodeTypes[string],
    [NODE_TYPE_FUNCTION_REF]: FunctionRefNode as NodeTypes[string],
};

// Custom edge with selection highlighting
const SelectableEdge: React.FC<EdgeProps> = ({
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    style,
    markerEnd,
    selected,
}) => {
    const [edgePath] = getBezierPath({
        sourceX,
        sourceY,
        sourcePosition,
        targetX,
        targetY,
        targetPosition,
    });

    // Apply selection styles
    const edgeStyle = {
        ...style,
        strokeWidth: selected ? 3 : 2,
        stroke: selected ? 'var(--g-color-line-brand)' : style?.stroke,
    };

    return <BaseEdge id={id} path={edgePath} style={edgeStyle} markerEnd={markerEnd} />;
};

const edgeTypes: EdgeTypes = {
    default: SelectableEdge,
    counterEdge: CounterEdge,
};

export interface PipelineGraphProps {
    initialNodes: PipelineNode[];
    initialEdges: PipelineEdge[];
    onNodesChange: (nodes: PipelineNode[]) => void;
    onEdgesChange: (edges: PipelineEdge[]) => void;
    onNodeDoubleClick?: (nodeId: string, nodeType: string) => void;
    /** When set, disables fitView and fixes zoom to 1 for horizontal scroll mode */
    minWidth?: number;
}

const PipelineGraphInner: React.FC<PipelineGraphProps> = ({
    initialNodes,
    initialEdges,
    onNodesChange,
    onEdgesChange,
    onNodeDoubleClick,
    minWidth,
}) => {
    const reactFlowWrapper = useRef<HTMLDivElement>(null);
    const connectingNodeId = useRef<string | null>(null);
    const connectionSuccessful = useRef(false);
    const { screenToFlowPosition, fitView } = useReactFlow();
    const graphId = useId();
    const [isReady, setIsReady] = useState(false);
    const [isFocused, setIsFocused] = useState(false);
    const [selectedNodeIds, setSelectedNodeIds] = useState<string[]>([]);
    const [selectedEdgeIds, setSelectedEdgeIds] = useState<string[]>([]);

    const [nodes, setNodes, onNodesChangeInternal] = useNodesState(initialNodes as Node[]);
    const [edges, setEdges, onEdgesChangeInternal] = useEdgesState(initialEdges as Edge[]);

    // Ref to track if we're syncing from parent to avoid cycles
    const isSyncingFromParent = useRef(false);

    // Create stable reference for node data only (excluding positions)
    const initialNodesDataKey = useMemo(
        () => JSON.stringify(initialNodes.map(n => ({ id: n.id, data: n.data, type: n.type }))),
        [initialNodes]
    );

    // Create stable reference for edge data
    const initialEdgesDataKey = useMemo(
        () => JSON.stringify(initialEdges.map(e => ({ id: e.id, source: e.source, target: e.target }))),
        [initialEdges]
    );

    // Sync external data changes (from dialog edits) to internal React Flow state
    useEffect(() => {
        if (nodes.length === 0) return;

        isSyncingFromParent.current = true;

        setNodes((currentNodes) => {
            const initialNodesMap = new Map(initialNodes.map(n => [n.id, n]));

            let hasChanges = false;
            const updatedNodes = currentNodes.map(node => {
                const initNode = initialNodesMap.get(node.id);
                if (initNode) {
                    const currentDataStr = JSON.stringify(node.data);
                    const initDataStr = JSON.stringify(initNode.data);
                    if (currentDataStr !== initDataStr) {
                        hasChanges = true;
                        return { ...node, data: initNode.data };
                    }
                }
                return node;
            });

            if (!hasChanges) {
                isSyncingFromParent.current = false;
                return currentNodes;
            }

            return updatedNodes;
        });

        setTimeout(() => {
            isSyncingFromParent.current = false;
        }, 0);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initialNodesDataKey]);

    // Sync external edge data changes to internal React Flow state
    useEffect(() => {
        if (edges.length === 0 && initialEdges.length === 0) return;

        isSyncingFromParent.current = true;

        setEdges((currentEdges) => {
            const initialEdgesMap = new Map(initialEdges.map(e => [e.id, e]));

            let hasChanges = false;
            const updatedEdges = currentEdges.map(edge => {
                const initEdge = initialEdgesMap.get(edge.id);
                if (initEdge) {
                    if (edge.source !== initEdge.source || edge.target !== initEdge.target) {
                        hasChanges = true;
                        return { ...edge, source: initEdge.source, target: initEdge.target };
                    }
                }
                return edge;
            });

            if (!hasChanges) {
                isSyncingFromParent.current = false;
                return currentEdges;
            }

            return updatedEdges;
        });

        setTimeout(() => {
            isSyncingFromParent.current = false;
        }, 0);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initialEdgesDataKey]);

    // Track selection changes
    const onSelectionChange = useCallback(({ nodes: selectedNodes, edges: selectedEdges }: OnSelectionChangeParams) => {
        setSelectedNodeIds(selectedNodes.map(n => n.id));
        setSelectedEdgeIds(selectedEdges.map(e => e.id));
    }, []);

    useOnSelectionChange({ onChange: onSelectionChange });

    // Track focus state
    useEffect(() => {
        const wrapper = reactFlowWrapper.current;
        if (!wrapper) return;

        const handleFocusIn = () => setIsFocused(true);
        const handleFocusOut = (e: FocusEvent) => {
            if (!wrapper.contains(e.relatedTarget as HTMLElement | null)) {
                setIsFocused(false);
            }
        };
        const handleClick = () => setIsFocused(true);

        wrapper.addEventListener('focusin', handleFocusIn);
        wrapper.addEventListener('focusout', handleFocusOut);
        wrapper.addEventListener('click', handleClick);

        return () => {
            wrapper.removeEventListener('focusin', handleFocusIn);
            wrapper.removeEventListener('focusout', handleFocusOut);
            wrapper.removeEventListener('click', handleClick);
        };
    }, []);

    // Custom delete handler - only delete if this graph is focused
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isFocused) return;

            if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
                return;
            }

            if (e.key === 'Delete' || e.key === 'Backspace') {
                e.preventDefault();
                e.stopPropagation();

                // Delete selected nodes (except INPUT and OUTPUT)
                if (selectedNodeIds.length > 0) {
                    const nodesToDelete = selectedNodeIds.filter(
                        id => id !== INPUT_NODE_ID && id !== OUTPUT_NODE_ID
                    );
                    if (nodesToDelete.length > 0) {
                        setNodes(nds => nds.filter(n => !nodesToDelete.includes(n.id)));
                        setEdges(eds => eds.filter(e =>
                            !nodesToDelete.includes(e.source) && !nodesToDelete.includes(e.target)
                        ));
                    }
                }

                // Delete selected edges
                if (selectedEdgeIds.length > 0) {
                    setEdges(eds => eds.filter(e => !selectedEdgeIds.includes(e.id)));
                }
            }
        };

        document.addEventListener('keydown', handleKeyDown, true);
        return () => document.removeEventListener('keydown', handleKeyDown, true);
    }, [isFocused, selectedNodeIds, selectedEdgeIds, setNodes, setEdges]);

    // Sync changes back to parent
    const handleNodesChange = useCallback((changes: Parameters<typeof onNodesChangeInternal>[0]) => {
        onNodesChangeInternal(changes);
    }, [onNodesChangeInternal]);

    const handleEdgesChange = useCallback((changes: Parameters<typeof onEdgesChangeInternal>[0]) => {
        onEdgesChangeInternal(changes);
    }, [onEdgesChangeInternal]);

    // Update parent when nodes/edges change
    useEffect(() => {
        if (isSyncingFromParent.current) return;
        onNodesChange(nodes as unknown as PipelineNode[]);
    }, [nodes, onNodesChange]);

    useEffect(() => {
        if (isSyncingFromParent.current) return;
        onEdgesChange(edges as unknown as PipelineEdge[]);
    }, [edges, onEdgesChange]);

    // Layout and fit view
    const handleLayoutAndFit = useCallback(() => {
        const layoutedNodes = layoutLinkedList(nodes as PipelineNode[], edges as PipelineEdge[]);
        setNodes(layoutedNodes as Node[]);
        setTimeout(() => {
            fitView({ padding: 0.4, maxZoom: 1.2, duration: 200 });
        }, 50);
    }, [nodes, edges, setNodes, fitView]);

    // Keyboard shortcut for layout (S key)
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isFocused) return;

            if (e.key === 's' || e.key === 'S') {
                if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
                    return;
                }
                e.preventDefault();
                handleLayoutAndFit();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [handleLayoutAndFit, isFocused]);

    const onConnect: OnConnect = useCallback(
        (params: Connection) => {
            // Don't allow connections to input or from output
            if (params.target === INPUT_NODE_ID || params.source === OUTPUT_NODE_ID) {
                return;
            }

            // For linked-list: check if source already has an outgoing edge or target has incoming
            const sourceHasOutgoing = edges.some(e => e.source === params.source);
            const targetHasIncoming = edges.some(e => e.target === params.target);

            if (sourceHasOutgoing || targetHasIncoming) {
                // In linked-list mode, we need to reconnect edges
                // Remove existing edges and create new connection
                setEdges((eds) => {
                    const filtered = eds.filter(e =>
                        e.source !== params.source && e.target !== params.target
                    );
                    return addEdge(params, filtered);
                });
            } else {
                connectionSuccessful.current = true;
                setEdges((eds) => addEdge(params, eds));
            }
        },
        [edges, setEdges]
    );

    const onConnectStartHandler: OnConnectStart = useCallback(
        (_, { nodeId }) => {
            connectingNodeId.current = nodeId;
            connectionSuccessful.current = false;
        },
        []
    );

    const onConnectEnd: OnConnectEnd = useCallback(
        (event) => {
            const sourceNodeId = connectingNodeId.current;
            if (!sourceNodeId) return;

            // Don't create new nodes when dragging from output
            if (sourceNodeId === OUTPUT_NODE_ID) {
                connectingNodeId.current = null;
                return;
            }

            // Don't create new node if connection was successful
            if (connectionSuccessful.current) {
                connectingNodeId.current = null;
                return;
            }

            const target = event.target as Element;
            const isDroppedOnNode = target.closest('.react-flow__node');
            const isDroppedOnHandle = target.closest('.react-flow__handle');

            if (!isDroppedOnNode && !isDroppedOnHandle && reactFlowWrapper.current) {
                const clientX = 'clientX' in event ? event.clientX : (event as TouchEvent).touches[0].clientX;
                const clientY = 'clientY' in event ? event.clientY : (event as TouchEvent).touches[0].clientY;

                const position = screenToFlowPosition({
                    x: clientX,
                    y: clientY,
                });

                const newNodeId = generateNodeId();
                const newNode: Node = {
                    id: newNodeId,
                    type: NODE_TYPE_FUNCTION_REF,
                    position,
                    data: { functionName: '' } as FunctionRefNodeData,
                };

                // For linked-list: insert the new node in the chain
                // Find what the source was connected to and reconnect
                const existingEdge = edges.find(e => e.source === sourceNodeId);

                if (existingEdge) {
                    // Insert new node between source and its target
                    const newEdge1: Edge = {
                        id: `edge-${sourceNodeId}-${newNodeId}`,
                        source: sourceNodeId,
                        target: newNodeId,
                    };
                    const newEdge2: Edge = {
                        id: `edge-${newNodeId}-${existingEdge.target}`,
                        source: newNodeId,
                        target: existingEdge.target,
                    };

                    setNodes((nds) => [...nds, newNode]);
                    setEdges((eds) => [...eds.filter(e => e.id !== existingEdge.id), newEdge1, newEdge2]);
                } else {
                    // Just add new node with edge from source
                    const newEdge: Edge = {
                        id: `edge-${sourceNodeId}-${newNodeId}`,
                        source: sourceNodeId,
                        target: newNodeId,
                    };

                    setNodes((nds) => [...nds, newNode]);
                    setEdges((eds) => [...eds, newEdge]);
                }
            }

            connectingNodeId.current = null;
        },
        [screenToFlowPosition, setNodes, setEdges, edges]
    );

    const handleNodeDoubleClick = useCallback(
        (_: React.MouseEvent, node: Node) => {
            if (onNodeDoubleClick) {
                onNodeDoubleClick(node.id, node.type || '');
            }
        },
        [onNodeDoubleClick]
    );

    const isValidConnection = useCallback((connection: Edge | Connection) => {
        // Don't allow connections to input
        if (connection.target === INPUT_NODE_ID) {
            return false;
        }
        // Don't allow connections from output
        if (connection.source === OUTPUT_NODE_ID) {
            return false;
        }
        // Don't allow self-connections
        if (connection.source === connection.target) {
            return false;
        }
        return true;
    }, []);

    const defaultEdgeOptions = useMemo(() => ({
        style: { strokeWidth: 2, stroke: 'var(--g-color-line-generic)' },
        type: 'default',
    }), []);

    const handleInit = useCallback(() => {
        setIsReady(true);
    }, []);

    return (
        <div
            ref={reactFlowWrapper}
            style={{ width: '100%', height: '100%', opacity: isReady ? 1 : 0, transition: 'opacity 0.1s ease-in' }}
            tabIndex={0}
            data-graph-id={graphId}
        >
            <ReactFlow
                id={graphId}
                nodes={nodes}
                edges={edges}
                onNodesChange={handleNodesChange}
                onEdgesChange={handleEdgesChange}
                onConnect={onConnect}
                onConnectStart={onConnectStartHandler}
                onConnectEnd={onConnectEnd}
                onNodeDoubleClick={handleNodeDoubleClick}
                onInit={handleInit}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                isValidConnection={isValidConnection}
                defaultEdgeOptions={defaultEdgeOptions}
                fitView={!minWidth}
                fitViewOptions={minWidth ? undefined : { padding: 0.4, maxZoom: 1.2 }}
                defaultViewport={minWidth ? { x: 0, y: 0, zoom: 1 } : undefined}
                minZoom={minWidth ? 1 : undefined}
                maxZoom={minWidth ? 1 : 1.2}
                deleteKeyCode={null}
                nodesDraggable
                nodesConnectable
                elementsSelectable
                panOnDrag={minWidth ? true : [1, 2]}
                panOnScroll={false}
                zoomOnScroll={false}
                zoomOnPinch={false}
                zoomOnDoubleClick={false}
                preventScrolling={false}
                connectOnClick={false}
            >
                <Panel position="top-left">
                    <Button
                        view="outlined"
                        size="s"
                        onClick={handleLayoutAndFit}
                        title="Layout graph (S)"
                    >
                        <Button.Icon>
                            <LayoutCells />
                        </Button.Icon>
                    </Button>
                </Panel>
                <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
            </ReactFlow>
        </div>
    );
};

export const PipelineGraph: React.FC<PipelineGraphProps> = (props) => {
    return (
        <ReactFlowProvider>
            <PipelineGraphInner {...props} />
        </ReactFlowProvider>
    );
};
