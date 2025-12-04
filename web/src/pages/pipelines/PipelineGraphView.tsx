import React, { useCallback, useEffect, useLayoutEffect, useRef, useState, useImperativeHandle, forwardRef } from 'react';
import type { Graph, TBlock, TBlockId } from '@gravity-ui/graph';
import { EAnchorType, GraphState, ConnectionLayer } from '@gravity-ui/graph';
import { GraphBlock, GraphCanvas, useGraph, useGraphEvent, useLayer } from '@gravity-ui/graph/react';
import { Button, Icon } from '@gravity-ui/uikit';
import { BarsAscendingAlignLeftArrowDown } from '@gravity-ui/icons';
import { toaster } from '../../utils';
import { PipelineBlock } from './PipelineBlock';
import type { FunctionId } from '../../api/pipelines';
import { pipelineGraphConfig } from './pipelineGraphConfig';
import { buildInitialEntities, createNewFunctionBlock } from './pipelineGraphBlocks';
import { usePipelineConnections } from './usePipelineConnections';
import {
    PIPELINE_BLOCK_IS,
    FUNCTION_BLOCK_WIDTH,
    TERMINAL_BLOCK_WIDTH,
    BLOCK_HEIGHT,
    BLOCK_SPACING,
    PROTECTED_BLOCK_IDS,
    ANIMATION_DURATION,
} from './pipelineGraphTypes';
import type { TPipelineBlock, BlockLayout } from './pipelineGraphTypes';
import './PipelineGraphView.css';

export interface PipelineFunctions {
    functions: FunctionId[];
}

export interface PipelineResult {
    isValid: boolean;
    functions: FunctionId[];
    orphanedBlocks: string[];
}

interface PipelineGraphViewProps {
    pipelineId: string;
    initialFunctions: FunctionId[];
    height?: number;
    onPipelineChange?: (result: PipelineResult) => void;
    onFunctionEdit?: (blockId: string, functionId: FunctionId | undefined) => void;
}

export interface PipelineGraphViewHandle {
    sortBlocks: () => void;
    getPipeline: () => PipelineResult;
    updateFunction: (blockId: string, functionId: FunctionId) => void;
}

export const PipelineGraphView = forwardRef<PipelineGraphViewHandle, PipelineGraphViewProps>(
    ({ pipelineId, initialFunctions, height = 300, onPipelineChange, onFunctionEdit }, ref): React.JSX.Element => {
        void pipelineId; // Reserved for future use
        const { graph, setEntities, start } = useGraph(pipelineGraphConfig);
        const [isGraphReady, setIsGraphReady] = useState(false);
        const [isFocused, setIsFocused] = useState(false);
        const animationFrameRef = useRef<number | null>(null);
        const containerRef = useRef<HTMLDivElement>(null);
        const hasInitialFitRef = useRef(false);

        useLayer(graph, ConnectionLayer, {});

        const { getConnections, hasOutgoingConnection, validateConnection } = usePipelineConnections(graph);

        // Build blocks and connections from functions array
        const { blocks: initialBlocks, connections: initialConnections } = React.useMemo(
            () => buildInitialEntities(initialFunctions),
            [initialFunctions]
        );

        // Handle connection creation
        useGraphEvent(
            graph,
            'connection-created',
            ({ sourceBlockId, sourceAnchorId, targetBlockId, targetAnchorId }: {
                sourceBlockId: TBlockId;
                sourceAnchorId?: string;
                targetBlockId: TBlockId;
                targetAnchorId?: string;
            }, event: CustomEvent) => {
                if (!sourceAnchorId || !targetAnchorId) {
                    return;
                }

                event.preventDefault();

                const sourceBlock = graph.rootStore.blocksList.getBlockState(sourceBlockId);
                const targetBlock = graph.rootStore.blocksList.getBlockState(targetBlockId);
                const sourceAnchor = sourceBlock?.getAnchorById(sourceAnchorId);
                const targetAnchor = targetBlock?.getAnchorById(targetAnchorId);

                if (sourceAnchor?.state.type !== EAnchorType.OUT || targetAnchor?.state.type !== EAnchorType.IN) {
                    toaster.warning('invalid-direction', 'Connect from output (right) to input (left)', 'Invalid connection');
                    return;
                }

                const validation = validateConnection(sourceBlockId, targetBlockId);
                if (!validation.valid) {
                    toaster.warning('invalid-connection', validation.message || 'Connection not allowed', 'Invalid connection');
                    return;
                }

                graph.api.addConnection({ sourceBlockId, sourceAnchorId, targetBlockId, targetAnchorId });
            }
        );

        // Handle creating new blocks by dragging from output anchor
        useGraphEvent(graph, 'connection-create-drop', ({ sourceBlockId, sourceAnchorId, targetBlockId, point }: {
            sourceBlockId: TBlockId;
            sourceAnchorId: string;
            targetBlockId?: TBlockId;
            point: { x: number; y: number };
        }) => {
            if (targetBlockId || !sourceAnchorId) {
                return;
            }

            const sourceBlockState = graph.rootStore.blocksList.getBlockState(sourceBlockId);
            const sourceAnchor = sourceBlockState?.getAnchorById(sourceAnchorId);
            if (!sourceAnchor || sourceAnchor.state.type !== EAnchorType.OUT) {
                toaster.info('invalid-creation', 'New blocks can only be created from output anchors', 'Cannot create block');
                return;
            }

            // Pipeline is linear - all blocks can only have one outgoing connection
            if (hasOutgoingConnection(sourceBlockId)) {
                toaster.warning('output-limit', 'Output already has a connection', 'Connection limit');
                return;
            }

            const newBlockId = `block-${Date.now()}`;
            const newBlock = createNewFunctionBlock(point.x, point.y - BLOCK_HEIGHT / 2, newBlockId);

            graph.api.addBlock(newBlock);
            graph.api.addConnection({
                sourceBlockId,
                sourceAnchorId,
                targetBlockId: newBlockId,
                targetAnchorId: `${newBlockId}-in`,
            });
        });

        // Animate block positions smoothly
        const animateBlockPositions = useCallback((targets: Map<TBlockId, BlockLayout>, onComplete?: () => void) => {
            if (!targets.size) {
                onComplete?.();
                return;
            }

            if (animationFrameRef.current !== null) {
                cancelAnimationFrame(animationFrameRef.current);
            }

            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];
            const initialPositions = new Map<TBlockId, { x: number; y: number }>();

            blockStates.forEach((blockState) => {
                const block = blockState.asTBlock();
                if (targets.has(block.id)) {
                    initialPositions.set(block.id, { x: block.x ?? 0, y: block.y ?? 0 });
                }
            });

            const startTime = performance.now();
            const easeInOutCubic = (t: number) => t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;

            const step = (now: number) => {
                const progress = Math.min((now - startTime) / ANIMATION_DURATION, 1);
                const easedProgress = easeInOutCubic(progress);

                initialPositions.forEach((start, blockId) => {
                    const target = targets.get(blockId);
                    if (!target) return;

                    graph.api.updateBlock({
                        id: blockId,
                        x: start.x + (target.x - start.x) * easedProgress,
                        y: start.y + (target.y - start.y) * easedProgress,
                        width: target.width,
                        height: target.height,
                    });
                });

                if (progress < 1) {
                    animationFrameRef.current = requestAnimationFrame(step);
                } else {
                    animationFrameRef.current = null;
                    onComplete?.();
                }
            };

            animationFrameRef.current = requestAnimationFrame(step);
        }, [graph]);

        const fitGraphToContent = useCallback((immediate = false) => {
            if (graph.state !== GraphState.READY) return;

            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];
            if (blockStates.length === 0) return;

            const blockIds = blockStates
                .map(blockState => blockState.asTBlock().id)
                .filter(Boolean) as TBlockId[];
            if (blockIds.length === 0) return;

            graph.zoomTo(blockIds, { padding: 50, transition: immediate ? 0 : 250 });
        }, [graph]);

        const layoutBlocks = useCallback((animate = true, fitImmediately = false) => {
            if (graph.state !== GraphState.READY) return;

            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];
            if (!blockStates.length) return;

            const connections = getConnections();

            // Build adjacency maps
            const outgoing = new Map<TBlockId, TBlockId>();
            const incoming = new Map<TBlockId, TBlockId>();

            connections.forEach(conn => {
                if (conn.sourceBlockId && conn.targetBlockId) {
                    outgoing.set(conn.sourceBlockId, conn.targetBlockId);
                    incoming.set(conn.targetBlockId, conn.sourceBlockId);
                }
            });

            const targetLayouts = new Map<TBlockId, BlockLayout>();
            const blockIds = blockStates.map(b => b.asTBlock().id);

            // Find the linear chain starting from INPUT
            const chain: TBlockId[] = [];
            let current: TBlockId | undefined = 'input';
            const visitedInChain = new Set<TBlockId>();

            while (current && !visitedInChain.has(current)) {
                if (current !== 'input' && current !== 'output') {
                    chain.push(current);
                }
                visitedInChain.add(current);
                current = outgoing.get(current);
            }

            // Find orphaned blocks (not in chain)
            const orphanedBlocks = blockIds.filter(id =>
                id !== 'input' && id !== 'output' && !visitedInChain.has(id)
            );

            // Layout INPUT block
            targetLayouts.set('input', {
                x: 0,
                y: 0,
                width: TERMINAL_BLOCK_WIDTH,
                height: BLOCK_HEIGHT,
            });

            // Layout chain blocks in a single line
            let x = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;
            chain.forEach(blockId => {
                targetLayouts.set(blockId, {
                    x,
                    y: 0,
                    width: FUNCTION_BLOCK_WIDTH,
                    height: BLOCK_HEIGHT,
                });
                x += FUNCTION_BLOCK_WIDTH + BLOCK_SPACING;
            });

            // Layout orphaned blocks below the main chain
            if (orphanedBlocks.length > 0) {
                const orphanY = BLOCK_HEIGHT + BLOCK_SPACING;
                let orphanX = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;

                orphanedBlocks.forEach(blockId => {
                    targetLayouts.set(blockId, {
                        x: orphanX,
                        y: orphanY,
                        width: FUNCTION_BLOCK_WIDTH,
                        height: BLOCK_HEIGHT,
                    });
                    orphanX += FUNCTION_BLOCK_WIDTH + BLOCK_SPACING;
                });
            }

            // Layout OUTPUT block at the end
            const outputX = chain.length > 0
                ? TERMINAL_BLOCK_WIDTH + BLOCK_SPACING + chain.length * (FUNCTION_BLOCK_WIDTH + BLOCK_SPACING)
                : TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;
            targetLayouts.set('output', {
                x: outputX,
                y: 0,
                width: TERMINAL_BLOCK_WIDTH,
                height: BLOCK_HEIGHT,
            });

            const afterLayout = () => {
                if (fitImmediately) {
                    fitGraphToContent(false);
                } else {
                    setTimeout(() => fitGraphToContent(false), 50);
                }
            };

            if (animate) {
                animateBlockPositions(targetLayouts, afterLayout);
            } else {
                targetLayouts.forEach((layout, blockId) => {
                    graph.api.updateBlock({ id: blockId, ...layout });
                });
                afterLayout();
            }
        }, [graph, getConnections, animateBlockPositions, fitGraphToContent]);

        const handleSortBlocks = useCallback(() => layoutBlocks(true, true), [layoutBlocks]);

        const getPipeline = useCallback((): PipelineResult => {
            if (graph.state !== GraphState.READY) {
                return { isValid: false, functions: [], orphanedBlocks: [] };
            }

            const connections = getConnections();
            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];

            // Build adjacency map
            const outgoing = new Map<TBlockId, TBlockId>();
            connections.forEach(conn => {
                if (conn.sourceBlockId && conn.targetBlockId) {
                    outgoing.set(conn.sourceBlockId, conn.targetBlockId);
                }
            });

            // Traverse the linear chain from INPUT to OUTPUT
            const functions: FunctionId[] = [];
            const visitedBlocks = new Set<TBlockId>();
            let current: TBlockId | undefined = outgoing.get('input');
            let reachesOutput = false;

            while (current && !visitedBlocks.has(current)) {
                if (current === 'output') {
                    reachesOutput = true;
                    break;
                }

                visitedBlocks.add(current);
                const blockState = blockStates.find(b => b.asTBlock().id === current);
                if (blockState) {
                    const block = blockState.asTBlock() as TPipelineBlock;
                    if (block.meta?.functionId) {
                        functions.push(block.meta.functionId);
                    } else {
                        functions.push({ name: block.name || 'Unknown' });
                    }
                }

                current = outgoing.get(current);
            }

            // Find orphaned blocks
            const nonTerminalBlocks = blockStates.filter(b => {
                const id = b.asTBlock().id;
                return id !== 'input' && id !== 'output';
            });

            const orphanedBlocks: string[] = [];
            for (const blockState of nonTerminalBlocks) {
                const blockId = blockState.asTBlock().id as string;
                if (!visitedBlocks.has(blockId)) {
                    orphanedBlocks.push(blockId);
                }
            }

            // Valid if: chain reaches OUTPUT and no orphaned blocks
            const isValid = reachesOutput && orphanedBlocks.length === 0;

            return { isValid, functions, orphanedBlocks };
        }, [graph, getConnections]);

        const notifyPipelineChange = useCallback(() => {
            if (onPipelineChange && graph.state === GraphState.READY) {
                const result = getPipeline();
                onPipelineChange(result);
            }
        }, [onPipelineChange, getPipeline, graph.state]);

        const updateFunction = useCallback((blockId: string, functionId: FunctionId) => {
            if (graph.state !== GraphState.READY) return;

            const blockState = graph.rootStore.blocksList.getBlockState(blockId);
            if (!blockState) return;

            const block = blockState.asTBlock() as TPipelineBlock;
            graph.api.updateBlock({
                id: blockId,
                name: functionId.name || 'Unknown',
                meta: { ...block.meta, description: 'function', functionId },
            });

            setTimeout(notifyPipelineChange, 50);
        }, [graph, notifyPipelineChange]);

        useImperativeHandle(ref, () => ({
            sortBlocks: handleSortBlocks,
            getPipeline,
            updateFunction,
        }), [handleSortBlocks, getPipeline, updateFunction]);

        useLayoutEffect(() => {
            setEntities({ blocks: initialBlocks, connections: initialConnections });
        }, [setEntities, initialBlocks, initialConnections]);

        useGraphEvent(graph, 'state-change', ({ state }: { state: GraphState }) => {
            if (state === GraphState.ATTACHED) start();
            if (state === GraphState.READY) setIsGraphReady(true);
        });

        useEffect(() => {
            if (isGraphReady) {
                const shouldFitImmediately = !hasInitialFitRef.current;
                layoutBlocks(false, shouldFitImmediately);
                if (shouldFitImmediately) hasInitialFitRef.current = true;
                setTimeout(notifyPipelineChange, 100);
            }
        }, [isGraphReady, layoutBlocks, notifyPipelineChange]);

        useEffect(() => {
            if (!isGraphReady) return;

            const blocksUnsub = graph.rootStore.blocksList.$blocks?.subscribe(() => {
                setTimeout(notifyPipelineChange, 50);
            });
            const connectionsUnsub = graph.rootStore.connectionsList.$connections?.subscribe(() => {
                setTimeout(notifyPipelineChange, 50);
            });

            return () => {
                blocksUnsub?.();
                connectionsUnsub?.();
            };
        }, [graph, isGraphReady, notifyPipelineChange]);

        const handleBlockDoubleClick = useCallback((blockId: string) => {
            // Terminal blocks are not editable
            if (blockId === 'output' || blockId === 'input') return;

            if (onFunctionEdit) {
                const blockState = graph.rootStore.blocksList.getBlockState(blockId);
                if (blockState) {
                    const block = blockState.asTBlock() as TPipelineBlock;
                    onFunctionEdit(blockId, block.meta?.functionId);
                }
            }
        }, [graph, onFunctionEdit]);

        useEffect(() => {
            if (graph.state !== GraphState.READY) return;

            const originalDeleteSelected = graph.api.deleteSelected.bind(graph.api);
            graph.api.deleteSelected = () => {
                graph.rootStore.blocksList.blockSelectionBucket?.deselect(PROTECTED_BLOCK_IDS, true);
                originalDeleteSelected();
            };

            return () => {
                graph.api.deleteSelected = originalDeleteSelected;
            };
        }, [graph, graph.state]);

        const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
            if (e.code === 'KeyS') {
                e.preventDefault();
                layoutBlocks(true, true);
                return;
            }

            if (e.code === 'Backspace' || e.code === 'Delete') {
                e.preventDefault();
                const selectedConnections = graph.rootStore.connectionsList.connectionSelectionBucket?.$selected?.value;
                if (selectedConnections && selectedConnections.size > 0) {
                    const states = graph.rootStore.connectionsList.getConnectionStates(Array.from(selectedConnections));
                    if (states.length > 0) {
                        graph.rootStore.connectionsList.deleteConnections(states);
                        graph.rootStore.connectionsList.resetSelection();
                        return;
                    }
                }
                graph.api.deleteSelected();
            }
        }, [graph, layoutBlocks]);

        useEffect(() => {
            return () => {
                if (animationFrameRef.current !== null) {
                    cancelAnimationFrame(animationFrameRef.current);
                }
            };
        }, []);

        const renderBlockFn = useCallback((graph: Graph, block: TBlock): React.JSX.Element => {
            if (block.is === PIPELINE_BLOCK_IS) {
                return (
                    <PipelineBlock
                        graph={graph}
                        block={block as TPipelineBlock}
                        onDoubleClick={handleBlockDoubleClick}
                    />
                );
            }

            return (
                <GraphBlock graph={graph} block={block}>
                    Unknown block {block.id}
                </GraphBlock>
            );
        }, [handleBlockDoubleClick]);

        return (
            <div
                ref={containerRef}
                className={`pipeline-view ${isFocused ? 'pipeline-view-focused' : ''}`}
                style={{
                    width: '100%',
                    height: height,
                    position: 'relative',
                    overflow: 'hidden',
                    borderRadius: '8px',
                    outline: 'none',
                }}
                tabIndex={0}
                onFocus={() => setIsFocused(true)}
                onBlur={() => setIsFocused(false)}
                onMouseEnter={() => {
                    if (!isFocused) containerRef.current?.focus();
                }}
                onKeyDown={handleKeyDown}
            >
                <div className="pipeline-graph-tools">
                    <Button size="s" onClick={handleSortBlocks} title="Sort blocks (S)">
                        <Icon data={BarsAscendingAlignLeftArrowDown} />
                    </Button>
                </div>
                <GraphCanvas graph={graph} renderBlock={renderBlockFn} />
            </div>
        );
    }
);

PipelineGraphView.displayName = 'PipelineGraphView';
