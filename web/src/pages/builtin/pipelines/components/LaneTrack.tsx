import React from 'react';
import type { FunctionRef, DragPayload } from '../types';
import { getDragPayload, InsertSlot, Endpoint, FlowLink, AddSlotButton } from '../../_shared/lane-editor';
import { FunctionRefCard } from './FunctionRefCard';
import type { InterpolatedCounterData } from '@yanet/core/hooks';
import { useLaneTrackDnD } from '@yanet/core/hooks';

interface LaneTrackProps {
    pipelineId: string;
    refs: FunctionRef[];
    dragState: { isDragging: boolean; dragPayload: DragPayload | null };
    counterMap: Map<string, InterpolatedCounterData>;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onDrop: (toIdx: number) => void;
    onOpenDrawer: (refId: string) => void;
    onRemoveRef: (refId: string) => void;
    onAddRef: () => void;
}

/**
 * The flex-wrap dropzone container for a pipeline's function references.
 * Handles dragover slot detection via DOM geometry.
 */
export const LaneTrack: React.FC<LaneTrackProps> = ({
    pipelineId,
    refs,
    dragState,
    counterMap,
    onDragStart,
    onDragEnd,
    onDrop,
    onOpenDrawer,
    onRemoveRef,
    onAddRef,
}) => {
    const { isDragging, dragPayload } = dragState;
    const isActiveDrag = isDragging && !!dragPayload;
    const isPipelineDrag = isActiveDrag && dragPayload.fromChainId === dragPayload.fromFnId;
    const isSamePipeline = isPipelineDrag && dragPayload.fromFnId === pipelineId;

    const fromRefIdx = isPipelineDrag && isSamePipeline
        ? dragPayload.fromModIdx
        : -1;

    const hiddenSlots = new Set<number>();
    if (fromRefIdx >= 0) {
        hiddenSlots.add(fromRefIdx);
        hiddenSlots.add(fromRefIdx + 1);
    }

    const { activeSlotIdx, containerRef, handleDragOver, handleDragLeave, handleDrop, handleDragEnd } =
        useLaneTrackDnD<DragPayload>({
            isItemDrag: isPipelineDrag,
            isActiveDrag,
            onDragEnd,
            getPayload: getDragPayload,
            acceptPayload: (p) => p.fromChainId === p.fromFnId,
            sameContainerSrcIdx: (p) => p.fromFnId === pipelineId ? p.fromModIdx : -1,
            onDropAt: onDrop,
        });

    return (
        <div
            className="pl-lane-track"
            ref={containerRef}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
        >
            <Endpoint kind="in" />

            {refs.map((ref, idx) => (
                <React.Fragment key={ref.id}>
                    {isPipelineDrag ? (
                        <InsertSlot
                            idx={idx}
                            active={activeSlotIdx === idx}
                            hidden={hiddenSlots.has(idx)}
                        />
                    ) : (
                        <FlowLink />
                    )}
                    <FunctionRefCard
                        ref_={ref}
                        pipelineId={pipelineId}
                        refIdx={idx}
                        isDragging={isPipelineDrag && dragPayload?.moduleId === ref.id}
                        isSourceDuringDrag={isPipelineDrag && isSamePipeline && dragPayload?.moduleId === ref.id}
                        isInvalidDragTarget={false}
                        counter={counterMap.get(ref.id)}
                        onDragStart={onDragStart}
                        onDragEnd={handleDragEnd}
                        onOpenDrawer={() => onOpenDrawer(ref.id)}
                        onRemove={() => onRemoveRef(ref.id)}
                    />
                </React.Fragment>
            ))}

            {refs.length === 0 && (
                <>
                    <FlowLink />
                    <div className="pl-lane-track__empty">
                        passthrough
                    </div>
                </>
            )}

            {isPipelineDrag && (
                <InsertSlot
                    idx={refs.length}
                    active={activeSlotIdx === refs.length}
                    hidden={hiddenSlots.has(refs.length)}
                />
            )}

            {!isActiveDrag && <FlowLink />}
            <Endpoint kind="out" />
            <AddSlotButton onClick={onAddRef} className="pl-add-ref-btn" label="Add function reference" />
        </div>
    );
};
