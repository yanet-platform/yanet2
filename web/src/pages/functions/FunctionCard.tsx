import React, { useState, useCallback, useEffect, useMemo } from 'react';
import { Box, Card, Alert } from '@gravity-ui/uikit';
import type { FunctionId } from '../../api/common';
import type { Function as APIFunction } from '../../api/functions';
import { API } from '../../api';
import { CardHeader, PageLoader } from '../../components';
import { FunctionGraph } from './FunctionGraph';
import { DeleteFunctionDialog, ModuleEditorDialog, SingleWeightEditorDialog } from './dialogs';
import type { ChainEditorResult } from './dialogs/SingleWeightEditorDialog';
import { CountersProvider } from '../../components';
import { useFunctionGraph, useModuleCounters } from './hooks';
import type { FunctionNode, FunctionEdge, ModuleNodeData } from './types';
import { NODE_TYPE_MODULE, INPUT_NODE_ID, OUTPUT_NODE_ID } from './types';
import '../FunctionsPage.css';

export interface FunctionCardProps {
    functionId: FunctionId;
    initialFunction?: APIFunction | null;
    loadFunction: (functionId: FunctionId) => Promise<APIFunction | null>;
    updateFunction: (func: APIFunction) => Promise<boolean>;
    deleteFunction: (functionId: FunctionId) => Promise<boolean>;
}

export const FunctionCard: React.FC<FunctionCardProps> = ({
    functionId,
    initialFunction,
    loadFunction,
    updateFunction,
    deleteFunction,
}) => {
    const [loading, setLoading] = useState(() => !initialFunction);
    const [saving, setSaving] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [moduleDialogOpen, setModuleDialogOpen] = useState(false);
    const [singleWeightDialogOpen, setSingleWeightDialogOpen] = useState(false);
    const [selectedModuleId, setSelectedModuleId] = useState<string | null>(null);
    const [selectedEdge, setSelectedEdge] = useState<FunctionEdge | null>(null);
    const [availableModuleTypes, setAvailableModuleTypes] = useState<string[]>([]);
    const [loadingModuleTypes, setLoadingModuleTypes] = useState(false);

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
    } = useFunctionGraph(initialFunction || undefined);

    // Build module info with chain names for counter fetching
    // Each module needs: nodeId, chainName, moduleType, moduleName
    const moduleInfoList = useMemo(() => {
        // Build a map of nodeId -> chainName by tracing paths from input edges
        const nodeToChain = new Map<string, string>();
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
        
        // DFS from each input edge to assign chain names to nodes
        const inputEdges = edges.filter(e => e.source === INPUT_NODE_ID);
        for (const edge of inputEdges) {
            const chainName = edge.data?.chainName || '';
            const visited = new Set<string>();
            
            const assignChain = (nodeId: string) => {
                if (visited.has(nodeId) || nodeId === OUTPUT_NODE_ID) return;
                visited.add(nodeId);
                
                // Only assign if not already assigned (first chain wins)
                if (!nodeToChain.has(nodeId)) {
                    nodeToChain.set(nodeId, chainName);
                }
                
                const neighbors = adjacency.get(nodeId) || [];
                for (const neighbor of neighbors) {
                    assignChain(neighbor);
                }
            };
            
            assignChain(edge.target);
        }
        
        // Build module info list
        return nodes
            .filter(n => n.type === NODE_TYPE_MODULE)
            .map(n => {
                const data = n.data as ModuleNodeData;
                const chainName = nodeToChain.get(n.id) || '';
                return {
                    nodeId: n.id,
                    chainName,
                    moduleType: data.type || '',
                    moduleName: data.name || '',
                };
            })
            .filter(info => info.moduleType && info.moduleName && info.chainName);
    }, [nodes, edges]);

    // Fetch and interpolate counters for all modules
    const { counters: moduleCounters } = useModuleCounters(
        functionId.name || '',
        moduleInfoList
    );

    // Load function data on mount (only if no initialFunction)
    useEffect(() => {
        // If we have initial data, no need to load
        if (initialFunction) {
            return;
        }

        let cancelled = false;

        const load = async (): Promise<void> => {
            setLoading(true);
            const func = await loadFunction(functionId);
            if (!cancelled && func) {
                loadFromApi(func);
            }
            if (!cancelled) {
                setLoading(false);
            }
        };
        load();
        return () => {
            cancelled = true;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [functionId, initialFunction]);

    // Load available module types when dialog opens
    useEffect(() => {
        if (moduleDialogOpen && availableModuleTypes.length === 0) {
            const loadModuleTypes = async () => {
                setLoadingModuleTypes(true);
                try {
                    const response = await API.inspect.inspect();
                    const dpModules = response.instance_info?.dp_modules ?? [];
                    const types = dpModules
                        .map(m => m.name)
                        .filter((name): name is string => !!name);
                    setAvailableModuleTypes(types);
                } catch {
                    // Ignore errors, user can still type custom values
                }
                setLoadingModuleTypes(false);
            };
            loadModuleTypes();
        }
    }, [moduleDialogOpen, availableModuleTypes.length]);

    const handleSave = useCallback(async () => {
        if (!isValid) {
            return;
        }

        setSaving(true);
        const func = toApi(functionId.name || '');
        const success = await updateFunction(func);
        if (success) {
            markClean();
        }
        setSaving(false);
    }, [isValid, toApi, functionId, updateFunction, markClean]);

    const handleDelete = useCallback(async () => {
        setDeleting(true);
        await deleteFunction(functionId);
        setDeleting(false);
        setDeleteDialogOpen(false);
    }, [deleteFunction, functionId]);

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
            <Card className="function-card">
                <Box className="function-card__content">
                    <PageLoader loading={loading} size="m" />
                </Box>
            </Card>
        );
    }

    return (
        <Card className="function-card">
            <Box className="function-card__content">
                <CardHeader
                    title={functionId.name || ''}
                    isDirty={isDirty}
                    onSave={handleSave}
                    onDelete={() => setDeleteDialogOpen(true)}
                    saveDisabled={!isValid}
                    saving={saving}
                />

                {/* Validation errors */}
                {validationErrors.length > 0 && (
                    <Box className="function-card__validation-errors">
                        <Alert theme="danger" message={validationErrors.join('. ')} />
                    </Box>
                )}

                {/* Graph */}
                <Box className="function-card__graph">
                    <CountersProvider counters={moduleCounters}>
                        <FunctionGraph
                            initialNodes={nodes}
                            initialEdges={edges}
                            onNodesChange={handleNodesChange}
                            onEdgesChange={handleEdgesChange}
                            onNodeDoubleClick={handleNodeDoubleClick}
                            onEdgeDoubleClick={handleEdgeDoubleClick}
                        />
                    </CountersProvider>
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
                availableModuleTypes={availableModuleTypes}
                loadingModuleTypes={loadingModuleTypes}
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
