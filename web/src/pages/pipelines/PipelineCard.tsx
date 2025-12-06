import React, { useState, useCallback, useEffect } from 'react';
import { Box, Text, Button, Flex, Card, Alert } from '@gravity-ui/uikit';
import { TrashBin, FloppyDisk } from '@gravity-ui/icons';
import type { PipelineId, Pipeline } from '../../api/pipelines';
import type { FunctionId } from '../../api/common';
import { PipelineGraph } from './PipelineGraph';
import { DeletePipelineDialog, FunctionRefEditorDialog } from './dialogs';
import { usePipelineGraph } from './hooks';
import type { PipelineNode, PipelineEdge, FunctionRefNodeData } from './types';
import { NODE_TYPE_FUNCTION_REF } from './types';
import { toaster } from '../../utils';

export interface PipelineCardProps {
    instance: number;
    pipelineId: PipelineId;
    loadPipeline: (instance: number, pipelineId: PipelineId) => Promise<Pipeline | null>;
    updatePipeline: (instance: number, pipeline: Pipeline) => Promise<boolean>;
    deletePipeline: (instance: number, pipelineId: PipelineId) => Promise<boolean>;
    loadFunctionList: (instance: number) => Promise<FunctionId[]>;
}

export const PipelineCard: React.FC<PipelineCardProps> = ({
    instance,
    pipelineId,
    loadPipeline,
    updatePipeline,
    deletePipeline,
    loadFunctionList,
}) => {
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [functionRefDialogOpen, setFunctionRefDialogOpen] = useState(false);
    const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
    const [availableFunctions, setAvailableFunctions] = useState<FunctionId[]>([]);
    const [loadingFunctions, setLoadingFunctions] = useState(false);
    
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
    } = usePipelineGraph();
    
    // Load pipeline data on mount
    useEffect(() => {
        const load = async (): Promise<void> => {
            setLoading(true);
            const pipeline = await loadPipeline(instance, pipelineId);
            if (pipeline) {
                loadFromApi(pipeline);
            }
            setLoading(false);
        };
        load();
    }, [instance, pipelineId, loadPipeline, loadFromApi]);
    
    // Load available functions when dialog opens
    useEffect(() => {
        if (functionRefDialogOpen && availableFunctions.length === 0) {
            const loadFunctions = async () => {
                setLoadingFunctions(true);
                const functions = await loadFunctionList(instance);
                setAvailableFunctions(functions);
                setLoadingFunctions(false);
            };
            loadFunctions();
        }
    }, [functionRefDialogOpen, availableFunctions.length, loadFunctionList, instance]);
    
    const handleSave = useCallback(async () => {
        if (!isValid) {
            return;
        }
        
        setSaving(true);
        try {
            const pipeline = toApi(pipelineId.name || '');
            const success = await updatePipeline(instance, pipeline);
            if (success) {
                markClean();
            }
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to build pipeline payload';
            toaster.error('pipeline-validation', message, err);
        }
        setSaving(false);
    }, [isValid, toApi, pipelineId, updatePipeline, instance, markClean]);
    
    const handleDelete = useCallback(async () => {
        setDeleting(true);
        await deletePipeline(instance, pipelineId);
        setDeleting(false);
        setDeleteDialogOpen(false);
    }, [deletePipeline, instance, pipelineId]);
    
    const handleNodeDoubleClick = useCallback((nodeId: string, nodeType: string) => {
        if (nodeType === NODE_TYPE_FUNCTION_REF) {
            setSelectedNodeId(nodeId);
            setFunctionRefDialogOpen(true);
        }
    }, []);
    
    const handleFunctionRefConfirm = useCallback((data: FunctionRefNodeData) => {
        if (!selectedNodeId) return;
        
        updateNode(selectedNodeId, data);
        setFunctionRefDialogOpen(false);
        setSelectedNodeId(null);
    }, [selectedNodeId, updateNode]);
    
    // Handle position-only changes (don't mark dirty)
    const handleNodesChange = useCallback((newNodes: PipelineNode[]) => {
        const oldNodeMap = new Map(nodes.map(n => [n.id, n]));
        
        const onlyNonDataChanges = nodes.length === newNodes.length && 
            newNodes.every(newNode => {
                const oldNode = oldNodeMap.get(newNode.id);
                if (!oldNode) return false;
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
    const handleEdgesChange = useCallback((newEdges: PipelineEdge[]) => {
        const oldEdgeMap = new Map(edges.map(e => [e.id, e]));
        
        const onlyNonDataChanges = edges.length === newEdges.length && 
            newEdges.every(newEdge => {
                const oldEdge = oldEdgeMap.get(newEdge.id);
                if (!oldEdge) return false;
                return oldEdge.source === newEdge.source && 
                    oldEdge.target === newEdge.target;
            });
        
        if (onlyNonDataChanges) {
            setEdgesWithoutDirty(newEdges);
        } else {
            setEdges(newEdges);
        }
    }, [edges, setEdges, setEdgesWithoutDirty]);
    
    const selectedNode = selectedNodeId
        ? nodes.find(n => n.id === selectedNodeId)
        : null;
    
    if (loading) {
        return (
            <Card style={{ marginBottom: '16px' }}>
                <Box style={{ padding: '16px' }}>
                    <Text>Loading pipeline {pipelineId.name}...</Text>
                </Box>
            </Card>
        );
    }
    
    return (
        <Card style={{ marginBottom: '16px' }}>
            <Box style={{ display: 'flex', flexDirection: 'column', height: '350px' }}>
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
                        <Text variant="subheader-2">{pipelineId.name}</Text>
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
                    <PipelineGraph
                        initialNodes={nodes}
                        initialEdges={edges}
                        onNodesChange={handleNodesChange}
                        onEdgesChange={handleEdgesChange}
                        onNodeDoubleClick={handleNodeDoubleClick}
                    />
                </Box>
            </Box>
            
            {/* Dialogs */}
            <DeletePipelineDialog
                open={deleteDialogOpen}
                onClose={() => setDeleteDialogOpen(false)}
                onConfirm={handleDelete}
                pipelineName={pipelineId.name || ''}
                loading={deleting}
            />
            
            <FunctionRefEditorDialog
                open={functionRefDialogOpen}
                onClose={() => {
                    setFunctionRefDialogOpen(false);
                    setSelectedNodeId(null);
                }}
                onConfirm={handleFunctionRefConfirm}
                initialData={
                    selectedNode?.type === NODE_TYPE_FUNCTION_REF
                        ? (selectedNode.data as FunctionRefNodeData)
                        : { functionName: '' }
                }
                availableFunctions={availableFunctions}
                loadingFunctions={loadingFunctions}
            />
        </Card>
    );
};

