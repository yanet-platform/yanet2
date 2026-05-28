import React, { useMemo } from 'react';
import type { Chain, DragPayload, FunctionsAction } from '../types';
import { LaneHeader } from './LaneHeader';
import { LaneTrack } from './LaneTrack';
import type { InterpolatedCounterData } from '../../../../hooks';

const MIN_LANE_HEIGHT = 76;
const MAX_LANE_HEIGHT = 132;

interface LaneProps {
    fnId: string;
    chain: Chain;
    totalWeight: number;
    dragState: { isDragging: boolean; dragPayload: DragPayload | null };
    counterMap: Map<string, InterpolatedCounterData>;
    siblingChainNames: string[];
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onDrop: (toChainId: string, toIdx: number) => void;
    dispatch: (action: FunctionsAction) => void;
    onAddModule: (chainId: string) => void;
    onOpenModuleDrawer: (moduleId: string, chainId: string) => void;
    onOpenChainDrawer: (chainId: string) => void;
}

/**
 * A single horizontal lane representing one chain in a function.
 * Height is proportional to weight (clamped to min/max).
 */
export const Lane: React.FC<LaneProps> = ({
    fnId,
    chain,
    totalWeight,
    dragState,
    counterMap,
    siblingChainNames,
    onDragStart,
    onDragEnd,
    onDrop,
    dispatch,
    onAddModule,
    onOpenModuleDrawer,
    onOpenChainDrawer,
}) => {
    const heightFraction = totalWeight > 0 ? chain.weight / totalWeight : 1;
    const laneHeight = Math.min(
        MAX_LANE_HEIGHT,
        Math.max(MIN_LANE_HEIGHT, MIN_LANE_HEIGHT + heightFraction * (MAX_LANE_HEIGHT - MIN_LANE_HEIGHT) * 1.6),
    );

    const aggCounter: InterpolatedCounterData | undefined = useMemo(() => {
        if (chain.modules.length === 0) return undefined;
        const c = counterMap.get(chain.modules[0].id);
        if (!c) return undefined;
        return { pps: c.pps, bps: c.bps };
    }, [chain.modules, counterMap]);

    const handleRename = (name: string): void => {
        dispatch({ type: 'UPDATE_CHAIN', fnId, chainId: chain.id, patch: { name } });
    };

    const handleWeightChange = (weight: number): void => {
        dispatch({ type: 'UPDATE_CHAIN', fnId, chainId: chain.id, patch: { weight } });
    };

    const handleRenameModule = (moduleId: string, name: string): void => {
        dispatch({ type: 'RENAME_MODULE', fnId, moduleId, name });
    };

    return (
        <div
            className="fn-lane"
            style={{ minHeight: `${laneHeight}px` }}
        >
            <LaneHeader
                chain={chain}
                totalWeight={totalWeight}
                aggCounter={aggCounter}
                siblingChainNames={siblingChainNames}
                onRename={handleRename}
                onWeightChange={handleWeightChange}
                onSelect={() => onOpenChainDrawer(chain.id)}
            />
            <LaneTrack
                fnId={fnId}
                chainId={chain.id}
                modules={chain.modules}
                dragState={dragState}
                counterMap={counterMap}
                onDragStart={onDragStart}
                onDragEnd={onDragEnd}
                onDrop={onDrop}
                onRenameModule={handleRenameModule}
                onOpenDrawer={moduleId => onOpenModuleDrawer(moduleId, chain.id)}
                onAddModule={() => onAddModule(chain.id)}
            />
        </div>
    );
};
