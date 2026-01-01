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
    EdgeLabelRenderer,
    getBezierPath,
    BaseEdge,
    useOnSelectionChange,
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

import { InputNode, OutputNode, ModuleNode } from './nodes';
import type { FunctionNode, FunctionEdge, ModuleNodeData, WeightedEdgeData } from './types';
import {
    NODE_TYPE_INPUT,
    NODE_TYPE_OUTPUT,
    NODE_TYPE_MODULE,
    INPUT_NODE_ID,
    OUTPUT_NODE_ID,
} from './types';
import { generateNodeId, layoutGraphTopologically } from './utils';

const nodeTypes: NodeTypes = {
    [NODE_TYPE_INPUT]: InputNode as NodeTypes[string],
    [NODE_TYPE_OUTPUT]: OutputNode as NodeTypes[string],
    [NODE_TYPE_MODULE]: ModuleNode as NodeTypes[string],
};

// Custom edge with weight label and selection highlighting
const WeightedEdge: React.FC<EdgeProps> = ({
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    data,
    style,
    markerEnd,
    selected,
}) => {
    const [edgePath, labelX, labelY] = getBezierPath({
        sourceX,
        sourceY,
        sourcePosition,
        targetX,
        targetY,
        targetPosition,
    });

    const edgeData = data as WeightedEdgeData | undefined;
    const weight = edgeData?.weight;
    const showWeight = weight !== undefined && weight !== null && weight !== '';

    // Apply selection styles
    const edgeStyle = {
        ...style,
        strokeWidth: selected ? 3 : 2,
        stroke: selected ? 'var(--g-color-line-brand)' : style?.stroke,
    };

    return (
        <>
            <BaseEdge id={id} path={edgePath} style={edgeStyle} markerEnd={markerEnd} />
            {showWeight && (
                <EdgeLabelRenderer>
                    <div
                        style={{
                            position: 'absolute',
                            transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
                            background: selected ? 'var(--g-color-base-brand)' : 'var(--g-color-base-background)',
                            padding: '2px 6px',
                            borderRadius: '4px',
                            fontSize: '11px',
                            fontWeight: 500,
                            color: selected ? 'var(--g-color-text-light-primary)' : 'var(--g-color-text-secondary)',
                            border: selected ? '1px solid var(--g-color-line-brand)' : '1px solid var(--g-color-line-generic)',
                            pointerEvents: 'none',
                        }}
                        className="nodrag nopan"
                    >
                        {String(weight)}
                    </div>
                </EdgeLabelRenderer>
            )}
        </>
    );
};

const edgeTypes: EdgeTypes = {
    default: WeightedEdge,
};

export interface FunctionGraphProps {
    initialNodes: FunctionNode[];
    initialEdges: FunctionEdge[];
    onNodesChange: (nodes: FunctionNode[]) => void;
    onEdgesChange: (edges: FunctionEdge[]) => void;
    onNodeDoubleClick?: (nodeId: string, nodeType: string) => void;
    onEdgeDoubleClick?: (edgeId: string, edge: FunctionEdge) => void;
}

const FunctionGraphInner: React.FC<FunctionGraphProps> = ({
    initialNodes,
    initialEdges,
    onNodesChange,
    onEdgesChange,
    onNodeDoubleClick,
    onEdgeDoubleClick,
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
    // This prevents re-running the effect when only positions change
    const initialNodesDataKey = useMemo(
        () => JSON.stringify(initialNodes.map(n => ({ id: n.id, data: n.data, type: n.type }))),
        [initialNodes]
    );
    
    // Create stable reference for edge data
    const initialEdgesDataKey = useMemo(
        () => JSON.stringify(initialEdges.map(e => ({ id: e.id, data: e.data, source: e.source, target: e.target }))),
        [initialEdges]
    );
    
    // Sync external data changes (from dialog edits) to internal React Flow state
    useEffect(() => {
        // Skip initial render
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
            
            // Only return new array if there were actual changes
            if (!hasChanges) {
                isSyncingFromParent.current = false;
                return currentNodes;
            }
            
            return updatedNodes;
        });
        
        // Reset flag after a tick to allow the state update to complete
        setTimeout(() => {
            isSyncingFromParent.current = false;
        }, 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initialNodesDataKey]); // Only depend on data key, not the full initialNodes array
    
    // Sync external edge data changes (from dialog edits) to internal React Flow state
    useEffect(() => {
        // Skip initial render
        if (edges.length === 0 && initialEdges.length === 0) return;
        
        isSyncingFromParent.current = true;
        
        setEdges((currentEdges) => {
            const initialEdgesMap = new Map(initialEdges.map(e => [e.id, e]));
            
            let hasChanges = false;
            const updatedEdges = currentEdges.map(edge => {
                const initEdge = initialEdgesMap.get(edge.id);
                if (initEdge) {
                    const currentDataStr = JSON.stringify(edge.data);
                    const initDataStr = JSON.stringify(initEdge.data);
                    if (currentDataStr !== initDataStr) {
                        hasChanges = true;
                        return { ...edge, data: initEdge.data };
                    }
                }
                return edge;
            });
            
            // Only return new array if there were actual changes
            if (!hasChanges) {
                isSyncingFromParent.current = false;
                return currentEdges;
            }
            
            return updatedEdges;
        });
        
        // Reset flag after a tick to allow the state update to complete
        setTimeout(() => {
            isSyncingFromParent.current = false;
        }, 0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initialEdgesDataKey]); // Only depend on data key, not the full initialEdges array
    
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
            // Only lose focus if the new focus target is outside this wrapper
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
            
            // Don't trigger if user is typing in an input
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
                        // Also delete edges connected to deleted nodes
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
        // Skip if we're syncing from parent to avoid cycles
        if (isSyncingFromParent.current) return;
        onNodesChange(nodes as unknown as FunctionNode[]);
    }, [nodes, onNodesChange]);
    
    useEffect(() => {
        // Skip if we're syncing from parent to avoid cycles
        if (isSyncingFromParent.current) return;
        onEdgesChange(edges as unknown as FunctionEdge[]);
    }, [edges, onEdgesChange]);
    
    // Layout and fit view
    const handleLayoutAndFit = useCallback(() => {
        const layoutedNodes = layoutGraphTopologically(nodes as FunctionNode[], edges as FunctionEdge[]);
        setNodes(layoutedNodes as Node[]);
        // Wait for nodes to be positioned before fitting
        setTimeout(() => {
            fitView({ padding: 0.4, maxZoom: 1.2, duration: 200 });
        }, 50);
    }, [nodes, edges, setNodes, fitView]);
    
    // Keyboard shortcut for layout (S key)
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isFocused) return;
            
            if (e.key === 's' || e.key === 'S') {
                // Don't trigger if user is typing in an input
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
            
            // Mark connection as successful to prevent creating new node in onConnectEnd
            connectionSuccessful.current = true;
            
            // Add weight data for edges from input
            const edgeData = params.source === INPUT_NODE_ID
                ? { weight: '1', chainName: `chain-${Date.now()}` }
                : undefined;
            
            setEdges((eds) => addEdge({ ...params, data: edgeData }, eds));
        },
        [setEdges]
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
            
            // Check if dropped on pane (not on a node or handle)
            const target = event.target as Element;
            const isDroppedOnNode = target.closest('.react-flow__node');
            const isDroppedOnHandle = target.closest('.react-flow__handle');
            
            if (!isDroppedOnNode && !isDroppedOnHandle && reactFlowWrapper.current) {
                // Get the position where the connection was dropped
                const clientX = 'clientX' in event ? event.clientX : (event as TouchEvent).touches[0].clientX;
                const clientY = 'clientY' in event ? event.clientY : (event as TouchEvent).touches[0].clientY;
                
                const position = screenToFlowPosition({
                    x: clientX,
                    y: clientY,
                });
                
                const newNodeId = generateNodeId();
                const newNode: Node = {
                    id: newNodeId,
                    type: NODE_TYPE_MODULE,
                    position,
                    data: { type: '', name: 'New Module' } as ModuleNodeData,
                };
                
                // Create edge from the connecting node to the new node
                const edgeData = sourceNodeId === INPUT_NODE_ID
                    ? { weight: '1', chainName: `chain-${Date.now()}` }
                    : undefined;
                
                const newEdge: Edge = {
                    id: `edge-${sourceNodeId}-${newNodeId}`,
                    source: sourceNodeId,
                    target: newNodeId,
                    data: edgeData,
                };
                
                // Add both node and edge together to ensure they're created atomically
                setNodes((nds) => [...nds, newNode]);
                setEdges((eds) => [...eds, newEdge]);
            }
            
            connectingNodeId.current = null;
        },
        [screenToFlowPosition, setNodes, setEdges]
    );
    
    const handleNodeDoubleClick = useCallback(
        (_: React.MouseEvent, node: Node) => {
            if (onNodeDoubleClick) {
                onNodeDoubleClick(node.id, node.type || '');
            }
        },
        [onNodeDoubleClick]
    );
    
    const handleEdgeDoubleClick = useCallback(
        (_: React.MouseEvent, edge: Edge) => {
            if (onEdgeDoubleClick) {
                onEdgeDoubleClick(edge.id, edge as unknown as FunctionEdge);
            }
        },
        [onEdgeDoubleClick]
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
                onEdgeDoubleClick={handleEdgeDoubleClick}
                onInit={handleInit}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                isValidConnection={isValidConnection}
                defaultEdgeOptions={defaultEdgeOptions}
                fitView
                fitViewOptions={{ padding: 0.4, maxZoom: 1.2 }}
                deleteKeyCode={null}
                nodesDraggable
                nodesConnectable
                elementsSelectable
                panOnDrag={[1, 2]}
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

export const FunctionGraph: React.FC<FunctionGraphProps> = (props) => {
    return (
        <ReactFlowProvider>
            <FunctionGraphInner {...props} />
        </ReactFlowProvider>
    );
};
