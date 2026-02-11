import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { Box, Card, Alert } from '@gravity-ui/uikit';
import type { PipelineId, Pipeline } from '../../api/pipelines';
import type { FunctionId } from '../../api/common';
import { CardHeader, CountersProvider, PageLoader } from '../../components';
import { PipelineGraph } from './PipelineGraph';
import { DeletePipelineDialog, FunctionRefEditorDialog } from './dialogs';
import { usePipelineGraph, useFunctionCounters, usePipelineCounters } from './hooks';
import type { PipelineNode, PipelineEdge, FunctionRefNodeData } from './types';
import { NODE_TYPE_FUNCTION_REF } from './types';
import { toaster } from '../../utils';
import './pipelines.css';

export interface PipelineCardProps {
    pipelineId: PipelineId;
    initialPipeline?: Pipeline | null;
    loadPipeline: (pipelineId: PipelineId) => Promise<Pipeline | null>;
    updatePipeline: (pipeline: Pipeline) => Promise<boolean>;
    deletePipeline: (pipelineId: PipelineId) => Promise<boolean>;
    loadFunctionList: () => Promise<FunctionId[]>;
}

export const PipelineCard: React.FC<PipelineCardProps> = ({
    pipelineId,
    initialPipeline,
    loadPipeline,
    updatePipeline,
    deletePipeline,
    loadFunctionList,
}) => {
    const [loading, setLoading] = useState(() => !initialPipeline);
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
    } = usePipelineGraph(initialPipeline || undefined);

    // Extract function names from nodes for counter fetching
    const functionNames = useMemo(() => {
        return nodes
            .filter(n => n.type === NODE_TYPE_FUNCTION_REF)
            .map(n => (n.data as FunctionRefNodeData).functionName)
            .filter(name => name && name.length > 0);
    }, [nodes]);

    // Calculate minimum width for the graph container based on actual node positions
    const graphMinWidth = useMemo(() => {
        const functionCount = nodes.filter(n => n.type === NODE_TYPE_FUNCTION_REF).length;
        if (functionCount < 3) return undefined;

        // Find the rightmost node position and add node width + padding
        const NODE_WIDTH = 140;
        const PADDING = 50;
        const maxX = Math.max(...nodes.map(n => n.position.x));
        return maxX + NODE_WIDTH + PADDING;
    }, [nodes]);

    const isFallthrough = functionNames.length === 0;

    // Fetch and interpolate counters for all functions
    const { counters: functionCounters } = useFunctionCounters(
        pipelineId.name || '',
        functionNames
    );

    const { counters: pipelineCounters } = usePipelineCounters(
        pipelineId.name || '',
        isFallthrough
    );

    const allCounters = useMemo(() => {
        const merged = new Map(functionCounters);
        for (const [key, value] of pipelineCounters) {
            merged.set(key, value);
        }
        return merged;
    }, [functionCounters, pipelineCounters]);

    // Load pipeline data on mount (only if no initialPipeline)
    useEffect(() => {
        // If we have initial data, no need to load
        if (initialPipeline) {
            return;
        }

        const load = async (): Promise<void> => {
            setLoading(true);
            const pipeline = await loadPipeline(pipelineId);
            if (pipeline) {
                loadFromApi(pipeline);
            }
            setLoading(false);
        };
        load();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [pipelineId, initialPipeline]);

    // Load available functions when dialog opens
    useEffect(() => {
        if (functionRefDialogOpen && availableFunctions.length === 0) {
            const loadFunctions = async () => {
                setLoadingFunctions(true);
                const functions = await loadFunctionList();
                setAvailableFunctions(functions);
                setLoadingFunctions(false);
            };
            loadFunctions();
        }
    }, [functionRefDialogOpen, availableFunctions.length, loadFunctionList]);

    const handleSave = useCallback(async () => {
        if (!isValid) {
            return;
        }

        setSaving(true);
        try {
            const pipeline = toApi(pipelineId.name || '');
            const success = await updatePipeline(pipeline);
            if (success) {
                markClean();
            }
        } catch (err) {
            const message = err instanceof Error ? err.message : 'Failed to build pipeline payload';
            toaster.error('pipeline-validation', message, err);
        }
        setSaving(false);
    }, [isValid, toApi, pipelineId, updatePipeline, markClean]);

    const handleDelete = useCallback(async () => {
        setDeleting(true);
        await deletePipeline(pipelineId);
        setDeleting(false);
        setDeleteDialogOpen(false);
    }, [deletePipeline, pipelineId]);

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
            <Card className="pipeline-card">
                <Box className="pipeline-card__content">
                    <PageLoader loading={loading} size="m" />
                </Box>
            </Card>
        );
    }

    return (
        <Card className="pipeline-card">
            <Box className="pipeline-card__content">
                <CardHeader
                    title={pipelineId.name || ''}
                    isDirty={isDirty}
                    onSave={handleSave}
                    onDelete={() => setDeleteDialogOpen(true)}
                    saveDisabled={!isValid}
                    saving={saving}
                />

                {/* Validation errors */}
                {validationErrors.length > 0 && (
                    <Box className="pipeline-card__validation-errors">
                        <Alert theme="danger" message={validationErrors[0]} />
                    </Box>
                )}

                {/* Graph */}
                <Box className="pipeline-card__graph">
                    <CountersProvider counters={allCounters}>
                        <PipelineGraph
                            initialNodes={nodes}
                            initialEdges={edges}
                            onNodesChange={handleNodesChange}
                            onEdgesChange={handleEdgesChange}
                            onNodeDoubleClick={handleNodeDoubleClick}
                            minWidth={graphMinWidth}
                        />
                    </CountersProvider>
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
