import React, { useState, useCallback, useEffect } from 'react';
import { Box, Text, Button, Flex, Card, Alert } from '@gravity-ui/uikit';
import { TrashBin, FloppyDisk } from '@gravity-ui/icons';
import type { FunctionId } from '../../api/common';
import type { Function as APIFunction } from '../../api/functions';
import { FunctionGraph } from './FunctionGraph';
import { DeleteFunctionDialog, ModuleEditorDialog, SingleWeightEditorDialog } from './dialogs';
import type { ChainEditorResult } from './dialogs/SingleWeightEditorDialog';
import { useFunctionGraph } from './hooks';
import type { FunctionNode, FunctionEdge, ModuleNodeData } from './types';
import { NODE_TYPE_MODULE, INPUT_NODE_ID } from './types';

export interface FunctionCardProps {
    instance: number;
    functionId: FunctionId;
    loadFunction: (instance: number, functionId: FunctionId) => Promise<APIFunction | null>;
    updateFunction: (instance: number, func: APIFunction) => Promise<boolean>;
    deleteFunction: (instance: number, functionId: FunctionId) => Promise<boolean>;
}

export const FunctionCard: React.FC<FunctionCardProps> = ({
    instance,
    functionId,
    loadFunction,
    updateFunction,
    deleteFunction,
}) => {
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [moduleDialogOpen, setModuleDialogOpen] = useState(false);
    const [singleWeightDialogOpen, setSingleWeightDialogOpen] = useState(false);
    const [selectedModuleId, setSelectedModuleId] = useState<string | null>(null);
    const [selectedEdge, setSelectedEdge] = useState<FunctionEdge | null>(null);
    
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
        loadFromApi,
        toApi,
        markClean,
    } = useFunctionGraph();
    
    // Load function data on mount
    useEffect(() => {
        const load = async (): Promise<void> => {
            setLoading(true);
            const func = await loadFunction(instance, functionId);
            if (func) {
                loadFromApi(func);
            }
            setLoading(false);
        };
        load();
    }, [instance, functionId, loadFunction, loadFromApi]);
    
    const handleSave = useCallback(async () => {
        if (!isValid) {
            return;
        }
        
        setSaving(true);
        const func = toApi(functionId.name || '');
        const success = await updateFunction(instance, func);
        if (success) {
            markClean();
        }
        setSaving(false);
    }, [isValid, toApi, functionId, updateFunction, instance, markClean]);
    
    const handleDelete = useCallback(async () => {
        setDeleting(true);
        await deleteFunction(instance, functionId);
        setDeleting(false);
        setDeleteDialogOpen(false);
    }, [deleteFunction, instance, functionId]);
    
    const handleNodeDoubleClick = useCallback((nodeId: string, nodeType: string) => {
        if (nodeType === NODE_TYPE_MODULE) {
            setSelectedModuleId(nodeId);
            setModuleDialogOpen(true);
        }
    }, []);
    
    const handleEdgeDoubleClick = useCallback((_edgeId: string, edge: FunctionEdge) => {
        // Only show weight editor for edges from input node
        if (edge.source === INPUT_NODE_ID) {
            setSelectedEdge(edge);
            setSingleWeightDialogOpen(true);
        }
    }, []);
    
    const handleModuleConfirm = useCallback((data: ModuleNodeData) => {
        if (!selectedModuleId) return;
        
        updateNode(selectedModuleId, data);
        setModuleDialogOpen(false);
        setSelectedModuleId(null);
    }, [selectedModuleId, updateNode]);
    
    const handleSingleWeightConfirm = useCallback((result: ChainEditorResult) => {
        if (!selectedEdge) return;
        
        const updatedEdges = edges.map(edge => {
            if (edge.id === selectedEdge.id) {
                return {
                    ...edge,
                    data: {
                        ...edge.data,
                        chainName: result.chainName,
                        weight: result.weight,
                    },
                };
            }
            return edge;
        });
        setEdges(updatedEdges);
        setSingleWeightDialogOpen(false);
        setSelectedEdge(null);
    }, [selectedEdge, edges, setEdges]);
    
    // Get existing chain names for uniqueness validation (excluding current edge)
    const existingChainNames = edges
        .filter(e => e.source === INPUT_NODE_ID && e.id !== selectedEdge?.id)
        .map(e => e.data?.chainName)
        .filter((name): name is string => !!name);
    
    // Handle position-only changes (don't mark dirty)
    const handleNodesChange = useCallback((newNodes: FunctionNode[]) => {
        // Build a map of old nodes by ID for efficient lookup
        const oldNodeMap = new Map(nodes.map(n => [n.id, n]));
        
        // Check if only positions/selection/dragging changed (same node set, same data/type)
        const onlyNonDataChanges = nodes.length === newNodes.length && 
            newNodes.every(newNode => {
                const oldNode = oldNodeMap.get(newNode.id);
                if (!oldNode) return false;
                // Check if data and type are the same (ignore position, selected, dragging)
                const oldData = JSON.stringify(oldNode.data);
                const newData = JSON.stringify(newNode.data);
                return oldData === newData && oldNode.type === newNode.type;
            });
        
        if (onlyNonDataChanges) {
            setNodesWithoutDirty(newNodes);
        } else {
            setNodes(newNodes);
        }
    }, [nodes, setNodes, setNodesWithoutDirty]);
    
    // Handle selection-only changes for edges (don't mark dirty)
    const handleEdgesChange = useCallback((newEdges: FunctionEdge[]) => {
        // Build a map of old edges by ID for efficient lookup
        const oldEdgeMap = new Map(edges.map(e => [e.id, e]));
        
        // Check if only selection changed (same edge set, same data/source/target)
        const onlyNonDataChanges = edges.length === newEdges.length && 
            newEdges.every(newEdge => {
                const oldEdge = oldEdgeMap.get(newEdge.id);
                if (!oldEdge) return false;
                // Check if data, source, and target are the same (ignore selected)
                const oldData = JSON.stringify(oldEdge.data);
                const newData = JSON.stringify(newEdge.data);
                return oldData === newData && 
                    oldEdge.source === newEdge.source && 
                    oldEdge.target === newEdge.target;
            });
        
        if (onlyNonDataChanges) {
            setEdgesWithoutDirty(newEdges);
        } else {
            setEdges(newEdges);
        }
    }, [edges, setEdges, setEdgesWithoutDirty]);
    
    const selectedModule = selectedModuleId
        ? nodes.find(n => n.id === selectedModuleId)
        : null;
    
    if (loading) {
        return (
            <Card style={{ marginBottom: '16px' }}>
                <Box style={{ padding: '16px' }}>
                    <Text>Loading function {functionId.name}...</Text>
                </Box>
            </Card>
        );
    }
    
    return (
        <Card style={{ marginBottom: '16px' }}>
            <Box style={{ display: 'flex', flexDirection: 'column', height: '450px' }}>
                {/* Header */}
                <Flex
                    alignItems="center"
                    justifyContent="space-between"
                    style={{
                        padding: '12px 16px',
                        borderBottom: '1px solid var(--g-color-line-generic)',
                    }}
                >
                    <Flex alignItems="center" gap={2}>
                        <Text variant="subheader-2">{functionId.name}</Text>
                        {isDirty && (
                            <Text variant="caption-1" color="secondary">
                                (unsaved changes)
                            </Text>
                        )}
                    </Flex>
                    <Flex gap={2}>
                        <Button
                            view="action"
                            onClick={handleSave}
                            disabled={!isValid || !isDirty}
                            loading={saving}
                        >
                            <Button.Icon>
                                <FloppyDisk />
                            </Button.Icon>
                            Save
                        </Button>
                        <Button
                            view="outlined-danger"
                            onClick={() => setDeleteDialogOpen(true)}
                        >
                            <Button.Icon>
                                <TrashBin />
                            </Button.Icon>
                            Delete
                        </Button>
                    </Flex>
                </Flex>
                
                {/* Validation errors */}
                {validationErrors.length > 0 && (
                    <Box style={{ padding: '8px 16px' }}>
                        <Alert theme="danger" message={validationErrors.join('. ')} />
                    </Box>
                )}
                
                {/* Graph */}
                <Box style={{ flex: 1, minHeight: 0 }}>
                    <FunctionGraph
                        initialNodes={nodes}
                        initialEdges={edges}
                        onNodesChange={handleNodesChange}
                        onEdgesChange={handleEdgesChange}
                        onNodeDoubleClick={handleNodeDoubleClick}
                        onEdgeDoubleClick={handleEdgeDoubleClick}
                    />
                </Box>
            </Box>
            
            {/* Dialogs */}
            <DeleteFunctionDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDelete}
                functionName={functionId.name || ''}
                loading={deleting}
            />
            
            <ModuleEditorDialog
                open={moduleDialogOpen}
                onClose={() => {
                    setModuleDialogOpen(false);
                    setSelectedModuleId(null);
                }}
                onConfirm={handleModuleConfirm}
                initialData={
                    selectedModule?.type === NODE_TYPE_MODULE
                        ? (selectedModule.data as ModuleNodeData)
                        : { type: '', name: '' }
                }
            />
            
            <SingleWeightEditorDialog
                open={singleWeightDialogOpen}
                onClose={() => {
                    setSingleWeightDialogOpen(false);
                    setSelectedEdge(null);
                }}
                onConfirm={handleSingleWeightConfirm}
                edgeId={selectedEdge?.id || ''}
                initialChainName={selectedEdge?.data?.chainName || ''}
                initialWeight={String(selectedEdge?.data?.weight ?? '1')}
                existingChainNames={existingChainNames}
            />
        </Card>
    );
};
