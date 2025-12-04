import React, { useCallback, useEffect, useLayoutEffect, useRef, useState, useImperativeHandle, forwardRef } from 'react';
import type { Graph, TBlock, TBlockId } from '@gravity-ui/graph';
import { EAnchorType, GraphState, ConnectionLayer } from '@gravity-ui/graph';
import { GraphBlock, GraphCanvas, useGraph, useGraphEvent, useLayer } from '@gravity-ui/graph/react';
import { Button, Icon } from '@gravity-ui/uikit';
import { BarsAscendingAlignLeftArrowDown } from '@gravity-ui/icons';
import { toaster } from '../../utils';
import { ActionBlock } from './ActionBlock';
import type { ModuleId } from '../../api/functions';
import { graphConfig } from './graphConfig';
import { buildInitialEntities, createNewActionBlock } from './graphBlocks';
import { useGraphConnections } from './useGraphConnections';
import {
    GRAVITY_ACTION_BLOCK_IS,
    ACTION_BLOCK_WIDTH,
    TERMINAL_BLOCK_WIDTH,
    BLOCK_HEIGHT,
    BLOCK_SPACING,
    PROTECTED_BLOCK_IDS,
    ANIMATION_DURATION,
} from './graphTypes';
import type { TGravityActionBlock, BlockLayout } from './graphTypes';
import './Graph.css';

export interface ChainPath {
    modules: ModuleId[];
    weight: number;
    /** First block ID in the chain (after INPUT), used to identify the chain */
    startBlockId?: string;
}

export interface ChainsResult {
    isValid: boolean;
    chains: ChainPath[];
    /** List of block IDs that are not connected (orphaned) */
    orphanedBlocks: string[];
}

interface GraphViewProps {
    functionId: string;
    initialChains: ChainPath[];
    height?: number;
    onChainsChange?: (result: ChainsResult) => void;
    onModuleEdit?: (blockId: string, moduleId: ModuleId | undefined) => void;
    onInputBlockEdit?: (chainIndex: number, currentWeight: number) => void;
}

export interface GraphViewHandle {
    sortBlocks: () => void;
    getChains: () => ChainsResult;
    updateModule: (blockId: string, moduleId: ModuleId) => void;
    updateChainWeight: (chainIndex: number, weight: number) => void;
}

export const GraphView = forwardRef<GraphViewHandle, GraphViewProps>(
    ({ functionId, initialChains, height = 300, onChainsChange, onModuleEdit, onInputBlockEdit }, ref): React.JSX.Element => {
        void functionId; // Reserved for future use
        const { graph, setEntities, start } = useGraph(graphConfig);
        const [isGraphReady, setIsGraphReady] = useState(false);
        const [isFocused, setIsFocused] = useState(false);
        const animationFrameRef = useRef<number | null>(null);
        const containerRef = useRef<HTMLDivElement>(null);
        const hasInitialFitRef = useRef(false);
        // Store chain weights by first block ID in each chain
        const chainWeightsRef = useRef<Map<string, number>>(new Map());

        useLayer(graph, ConnectionLayer, {});

        const { getConnections, hasOutgoingConnection, validateConnection } = useGraphConnections(graph);

        // Build blocks and connections from chains array
        const { blocks: initialBlocks, connections: initialConnections, chainWeights } = React.useMemo(
            () => buildInitialEntities(initialChains),
            [initialChains]
        );

        // Initialize chain weights from initial data
        useEffect(() => {
            // Convert TBlockId keys to string for the ref
            const stringKeyMap = new Map<string, number>();
            chainWeights.forEach((value, key) => {
                stringKeyMap.set(String(key), value);
            });
            chainWeightsRef.current = stringKeyMap;
        }, [chainWeights]);

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

            // INPUT block can have multiple outgoing connections (for multiple chains)
            // Other blocks can only have one outgoing connection
            if (sourceBlockId !== 'input' && hasOutgoingConnection(sourceBlockId)) {
                toaster.warning('output-limit', 'Output already has a connection', 'Connection limit');
                return;
            }

            const newBlockId = `block-${Date.now()}`;
            const newBlock = createNewActionBlock(point.x, point.y - BLOCK_HEIGHT / 2, newBlockId);

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

            // Zoom to actual block bounds to avoid stale usableRect caching after manual camera moves
            graph.zoomTo(blockIds, { padding: 50, transition: immediate ? 0 : 250 });
        }, [graph]);

        const layoutBlocks = useCallback((animate = true, fitImmediately = false) => {
            if (graph.state !== GraphState.READY) return;

            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];
            if (!blockStates.length) return;

            const connections = getConnections();

            // Build adjacency maps (multi-valued for multiple chains)
            const outgoing = new Map<TBlockId, TBlockId[]>();
            const incoming = new Map<TBlockId, TBlockId[]>();

            connections.forEach(conn => {
                if (conn.sourceBlockId && conn.targetBlockId) {
                    if (!outgoing.has(conn.sourceBlockId)) {
                        outgoing.set(conn.sourceBlockId, []);
                    }
                    outgoing.get(conn.sourceBlockId)!.push(conn.targetBlockId);

                    if (!incoming.has(conn.targetBlockId)) {
                        incoming.set(conn.targetBlockId, []);
                    }
                    incoming.get(conn.targetBlockId)!.push(conn.sourceBlockId);
                }
            });

            const targetLayouts = new Map<TBlockId, BlockLayout>();
            const blockIds = blockStates.map(b => b.asTBlock().id);

            // Find all chains starting from INPUT
            const inputTargets = outgoing.get('input') || [];
            const chains: TBlockId[][] = [];
            const visitedInChains = new Set<TBlockId>();

            for (const startBlockId of inputTargets) {
                const chain: TBlockId[] = [];
                let current: TBlockId | undefined = startBlockId;

                while (current && current !== 'output' && !visitedInChains.has(current)) {
                    chain.push(current);
                    visitedInChains.add(current);
                    const targets = outgoing.get(current);
                    current = targets?.[0];
                }

                if (chain.length > 0) {
                    chains.push(chain);
                }
            }

            // Find orphaned blocks (not in any chain)
            const orphanedBlocks = blockIds.filter(id =>
                id !== 'input' && id !== 'output' && !visitedInChains.has(id)
            );

            // Calculate layout
            const chainSpacingY = BLOCK_HEIGHT + BLOCK_SPACING;
            const totalChains = chains.length + (orphanedBlocks.length > 0 ? 1 : 0);
            const totalHeight = totalChains * chainSpacingY - BLOCK_SPACING;
            const startY = -totalHeight / 2 + BLOCK_HEIGHT / 2;

            // Find max chain length for output position
            const maxChainLength = Math.max(...chains.map(c => c.length), 0);

            // Layout INPUT block
            targetLayouts.set('input', {
                x: 0,
                y: 0,
                width: TERMINAL_BLOCK_WIDTH,
                height: BLOCK_HEIGHT,
            });

            // Layout each chain
            chains.forEach((chain, chainIndex) => {
                const chainY = startY + chainIndex * chainSpacingY;
                let x = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;

                chain.forEach(blockId => {
                    targetLayouts.set(blockId, {
                        x,
                        y: chainY,
                        width: ACTION_BLOCK_WIDTH,
                        height: BLOCK_HEIGHT,
                    });
                    x += ACTION_BLOCK_WIDTH + BLOCK_SPACING;
                });
            });

            // Layout orphaned blocks at the bottom
            if (orphanedBlocks.length > 0) {
                const orphanY = startY + chains.length * chainSpacingY;
                let x = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;

                orphanedBlocks.forEach(blockId => {
                    targetLayouts.set(blockId, {
                        x,
                        y: orphanY,
                        width: ACTION_BLOCK_WIDTH,
                        height: BLOCK_HEIGHT,
                    });
                    x += ACTION_BLOCK_WIDTH + BLOCK_SPACING;
                });
            }

            // Layout OUTPUT block
            const outputX = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING +
                (maxChainLength > 0 ? (ACTION_BLOCK_WIDTH + BLOCK_SPACING) * maxChainLength : 0);
            targetLayouts.set('output', {
                x: outputX,
                y: 0,
                width: TERMINAL_BLOCK_WIDTH,
                height: BLOCK_HEIGHT,
            });

            const afterLayout = () => {
                // fitImmediately means no extra delay, but still animate the zoom
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

        const getChains = useCallback((): ChainsResult => {
            if (graph.state !== GraphState.READY) {
                return { isValid: false, chains: [], orphanedBlocks: [] };
            }

            const connections = getConnections();
            const blockStates = graph.rootStore.blocksList.$blocks?.value ?? [];

            // Build adjacency map (multi-valued for multiple chains)
            const outgoing = new Map<TBlockId, TBlockId[]>();
            connections.forEach(conn => {
                if (conn.sourceBlockId && conn.targetBlockId) {
                    if (!outgoing.has(conn.sourceBlockId)) {
                        outgoing.set(conn.sourceBlockId, []);
                    }
                    outgoing.get(conn.sourceBlockId)!.push(conn.targetBlockId);
                }
            });

            // Get all chains starting from INPUT
            const inputTargets = outgoing.get('input') || [];
            if (inputTargets.length === 0) {
                return { isValid: false, chains: [], orphanedBlocks: [] };
            }

            const chains: ChainPath[] = [];
            const visitedBlocks = new Set<TBlockId>();

            // Traverse each chain from INPUT
            for (const startBlockId of inputTargets) {
                const chainModules: ModuleId[] = [];
                let current: TBlockId | undefined = startBlockId;
                const chainVisited = new Set<TBlockId>();

                while (current && current !== 'output' && !chainVisited.has(current)) {
                    chainVisited.add(current);
                    visitedBlocks.add(current);

                    const blockState = blockStates.find(b => b.asTBlock().id === current);
                    if (blockState) {
                        const block = blockState.asTBlock() as TGravityActionBlock;
                        if (block.meta?.moduleId) {
                            chainModules.push(block.meta.moduleId);
                        } else {
                            chainModules.push({ type: block.meta?.description || 'module', name: block.name });
                        }
                    }

                    const targets = outgoing.get(current);
                    current = targets?.[0]; // Regular blocks should have at most one outgoing
                }

                // Check if this chain reaches OUTPUT
                const reachesOutput = current === 'output';
                if (reachesOutput) {
                    // Get weight for this chain (by first block ID)
                    const weight = chainWeightsRef.current.get(startBlockId as string) ?? 1;
                    chains.push({
                        modules: chainModules,
                        weight,
                        startBlockId: startBlockId as string,
                    });
                }
            }

            // Find orphaned blocks (not part of any chain)
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

            // Valid if: at least one chain reaches OUTPUT and no orphaned blocks
            const isValid = chains.length > 0 && orphanedBlocks.length === 0;

            return { isValid, chains, orphanedBlocks };
        }, [graph, getConnections]);

        const notifyChainsChange = useCallback(() => {
            if (onChainsChange && graph.state === GraphState.READY) {
                const result = getChains();
                onChainsChange(result);
            }
        }, [onChainsChange, getChains, graph.state]);

        const updateModule = useCallback((blockId: string, moduleId: ModuleId) => {
            if (graph.state !== GraphState.READY) return;

            const blockState = graph.rootStore.blocksList.getBlockState(blockId);
            if (!blockState) return;

            const block = blockState.asTBlock() as TGravityActionBlock;
            graph.api.updateBlock({
                id: blockId,
                name: moduleId.name || 'Unknown',
                meta: { ...block.meta, description: moduleId.type || 'module', moduleId },
            });

            setTimeout(notifyChainsChange, 50);
        }, [graph, notifyChainsChange]);

        const updateChainWeight = useCallback((chainIndex: number, weight: number) => {
            const result = getChains();
            const chain = result.chains[chainIndex];
            if (chain?.startBlockId) {
                chainWeightsRef.current.set(chain.startBlockId, weight);
                setTimeout(notifyChainsChange, 50);
            }
        }, [getChains, notifyChainsChange]);

        useImperativeHandle(ref, () => ({
            sortBlocks: handleSortBlocks,
            getChains,
            updateModule,
            updateChainWeight,
        }), [handleSortBlocks, getChains, updateModule, updateChainWeight]);

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
                setTimeout(notifyChainsChange, 100);
            }
        }, [isGraphReady, layoutBlocks, notifyChainsChange]);

        useEffect(() => {
            if (!isGraphReady) return;

            const blocksUnsub = graph.rootStore.blocksList.$blocks?.subscribe(() => {
                setTimeout(notifyChainsChange, 50);
            });
            const connectionsUnsub = graph.rootStore.connectionsList.$connections?.subscribe(() => {
                setTimeout(notifyChainsChange, 50);
            });

            return () => {
                blocksUnsub?.();
                connectionsUnsub?.();
            };
        }, [graph, isGraphReady, notifyChainsChange]);

        const handleBlockDoubleClick = useCallback((blockId: string) => {
            // OUTPUT block is not editable
            if (blockId === 'output') return;

            // INPUT block - open chain weight editor
            if (blockId === 'input') {
                if (onInputBlockEdit) {
                    // Find which chain to edit (first one by default, or could show a selector)
                    const result = getChains();
                    if (result.chains.length > 0) {
                        onInputBlockEdit(0, result.chains[0].weight);
                    }
                }
                return;
            }

            if (onModuleEdit) {
                const blockState = graph.rootStore.blocksList.getBlockState(blockId);
                if (blockState) {
                    const block = blockState.asTBlock() as TGravityActionBlock;
                    onModuleEdit(blockId, block.meta?.moduleId);
                }
            }
        }, [graph, onModuleEdit, onInputBlockEdit, getChains]);

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
        }, [graph, handleSortBlocks]);

        useEffect(() => {
            return () => {
                if (animationFrameRef.current !== null) {
                    cancelAnimationFrame(animationFrameRef.current);
                }
            };
        }, []);

        const renderBlockFn = useCallback((graph: Graph, block: TBlock): React.JSX.Element => {
            if (block.is === GRAVITY_ACTION_BLOCK_IS) {
                return (
                    <ActionBlock
                        graph={graph}
                        block={block as TGravityActionBlock}
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
                className={`view ${isFocused ? 'view-focused' : ''}`}
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
                <div className="graph-tools">
                    <Button size="s" onClick={handleSortBlocks} title="Sort blocks (S)">
                        <Icon data={BarsAscendingAlignLeftArrowDown} />
                    </Button>
                </div>
                <GraphCanvas graph={graph} renderBlock={renderBlockFn} />
            </div>
        );
    }
);

GraphView.displayName = 'GraphView';
