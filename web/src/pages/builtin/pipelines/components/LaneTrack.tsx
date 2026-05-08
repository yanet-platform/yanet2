import React, { useState, useCallback, useRef, useEffect } from 'react';
import type { FunctionRef, DragPayload } from '../types';
import { getDragPayload, InsertSlot, Endpoint, FlowLink } from '../../_shared/lane-editor';
import { FunctionRefCard } from './FunctionRefCard';
import { AddFunctionRefButton } from './AddFunctionRefButton';
import type { InterpolatedCounterData } from '../../../../hooks';

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
    const [activeSlotIdx, setActiveSlotIdx] = useState<number | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const cancelledByEscRef = useRef(false);

    const { isDragging, dragPayload } = dragState;
    const isActiveDrag = isDragging && !!dragPayload;
    const isSamePipeline = isActiveDrag && dragPayload.fromFnId === pipelineId;

    const fromRefIdx = isActiveDrag && dragPayload.fromChainId === pipelineId
        ? dragPayload.fromModIdx
        : -1;

    const hiddenSlots = new Set<number>();
    if (fromRefIdx >= 0) {
        hiddenSlots.add(fromRefIdx);
        hiddenSlots.add(fromRefIdx + 1);
    }

    useEffect(() => {
        if (!isActiveDrag) {
            cancelledByEscRef.current = false;
            return;
        }
        const handleKeyDown = (e: KeyboardEvent): void => {
            if (e.key === 'Escape' && isActiveDrag) {
                cancelledByEscRef.current = true;
                setActiveSlotIdx(null);
                onDragEnd();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [isActiveDrag, onDragEnd]);

    const handleDragOver = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        if (!isSamePipeline) {
            return;
        }
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';

        const container = containerRef.current;
        if (!container) {
            return;
        }

        const slots = container.querySelectorAll<HTMLElement>('[data-slot-idx]');
        if (slots.length === 0) {
            return;
        }

        const cx = e.clientX;
        const cy = e.clientY;
        let nearestIdx = 0;
        let nearestDist = Infinity;

        slots.forEach(slot => {
            const rect = slot.getBoundingClientRect();
            const slotCx = rect.left + rect.width / 2;
            const slotCy = rect.top + rect.height / 2;
            const dist = Math.sqrt((cx - slotCx) ** 2 + (cy - slotCy) ** 2);
            const idx = parseInt(slot.getAttribute('data-slot-idx') ?? '0', 10);
            if (dist < nearestDist) {
                nearestDist = dist;
                nearestIdx = idx;
            }
        });

        setActiveSlotIdx(nearestIdx);
    }, [isSamePipeline]);

    const handleDragLeave = useCallback((): void => {
        setActiveSlotIdx(null);
    }, []);

    const handleDrop = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        e.preventDefault();
        setActiveSlotIdx(null);

        if (cancelledByEscRef.current) {
            cancelledByEscRef.current = false;
            return;
        }

        const payload = getDragPayload();
        if (!payload || payload.fromFnId !== pipelineId) {
            return;
        }

        if (activeSlotIdx === null) {
            return;
        }

        const toIdx = activeSlotIdx;
        const src = payload.fromModIdx;
        if (toIdx === src || toIdx === src + 1) {
            return;
        }

        onDrop(toIdx);
    }, [pipelineId, activeSlotIdx, onDrop]);

    const handleDragEnd = useCallback((): void => {
        setActiveSlotIdx(null);
        if (!cancelledByEscRef.current) {
            onDragEnd();
        }
    }, [onDragEnd]);

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
                    {isActiveDrag && isSamePipeline ? (
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
                        isDragging={isActiveDrag && dragPayload?.moduleId === ref.id}
                        isSourceDuringDrag={isActiveDrag && isSamePipeline && dragPayload?.moduleId === ref.id}
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

            {isActiveDrag && isSamePipeline && (
                <InsertSlot
                    idx={refs.length}
                    active={activeSlotIdx === refs.length}
                    hidden={hiddenSlots.has(refs.length)}
                />
            )}

            {!isActiveDrag && <FlowLink />}
            <Endpoint kind="out" />
            <AddFunctionRefButton onClick={onAddRef} />
        </div>
    );
};
