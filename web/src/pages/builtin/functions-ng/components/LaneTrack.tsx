import React, { useState, useCallback, useRef, useEffect } from 'react';
import type { Module, DragPayload } from '../types';
import { getDragPayload } from '../hooks/useDragState';
import { ModuleCard } from './ModuleCard';
import { InsertSlot } from './InsertSlot';
import { Endpoint } from './Endpoint';
import { FlowLink } from './FlowLink';
import { AddModuleButton } from './AddModuleButton';
import type { InterpolatedCounterData } from '../../../../hooks';

interface LaneTrackProps {
    fnId: string;
    chainId: string;
    modules: Module[];
    dragState: { isDragging: boolean; dragPayload: DragPayload | null };
    counterMap: Map<string, InterpolatedCounterData>;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onDrop: (toChainId: string, toIdx: number) => void;
    onRenameModule: (moduleId: string, name: string) => void;
    onOpenDrawer: (moduleId: string) => void;
    onAddModule: () => void;
}

/**
 * The flex-wrap dropzone container for a chain's modules.
 * Handles dragover slot detection via DOM geometry, validates cross-function drops.
 */
export const LaneTrack: React.FC<LaneTrackProps> = ({
    fnId,
    chainId,
    modules,
    dragState,
    counterMap,
    onDragStart,
    onDragEnd,
    onDrop,
    onRenameModule,
    onOpenDrawer,
    onAddModule,
}) => {
    const [activeSlotIdx, setActiveSlotIdx] = useState<number | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    // When true, Esc was pressed during a drag — ignore the next drop event.
    const cancelledByEscRef = useRef(false);

    const { isDragging, dragPayload } = dragState;
    const isActiveDrag = isDragging && !!dragPayload;
    const isSameFn = isActiveDrag && dragPayload.fromFnId === fnId;
    const isCrossFunction = isActiveDrag && dragPayload.fromFnId !== fnId;

    const fromModIdx = isActiveDrag && dragPayload.fromChainId === chainId
        ? dragPayload.fromModIdx
        : -1;

    const hiddenSlots = new Set<number>();
    if (fromModIdx >= 0) {
        hiddenSlots.add(fromModIdx);
        hiddenSlots.add(fromModIdx + 1);
    }

    // Esc key cancels the active drag.
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
        if (!isSameFn) {
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
    }, [isSameFn]);

    const handleDragLeave = useCallback((): void => {
        setActiveSlotIdx(null);
    }, []);

    const handleDrop = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        e.preventDefault();
        setActiveSlotIdx(null);

        // Cancelled by Esc — reset flag and do nothing.
        if (cancelledByEscRef.current) {
            cancelledByEscRef.current = false;
            return;
        }

        const payload = getDragPayload();
        if (!payload || payload.fromFnId !== fnId) {
            return;
        }

        // No slot was hovered — user dropped outside any slot, treat as cancel.
        if (activeSlotIdx === null) {
            return;
        }

        const toIdx = activeSlotIdx;

        // Drop resolves to the source position — no-op.
        if (payload.fromChainId === chainId) {
            const src = payload.fromModIdx;
            if (toIdx === src || toIdx === src + 1) {
                return;
            }
        }

        onDrop(chainId, toIdx);
    }, [fnId, chainId, activeSlotIdx, onDrop]);

    const handleDragEnd = useCallback((): void => {
        setActiveSlotIdx(null);
        if (!cancelledByEscRef.current) {
            onDragEnd();
        }
    }, [onDragEnd]);

    const siblingNames = modules.map(m => m.name);

    return (
        <div
            className={[
                'fng-lane-track',
                isActiveDrag && isCrossFunction ? 'fng-lane-track--reject' : '',
            ].filter(Boolean).join(' ')}
            ref={containerRef}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
        >
            <Endpoint kind="in" />

            {modules.map((m, idx) => (
                <React.Fragment key={m.id}>
                    {isActiveDrag && isSameFn ? (
                        <InsertSlot
                            idx={idx}
                            active={activeSlotIdx === idx}
                            hidden={hiddenSlots.has(idx)}
                        />
                    ) : (
                        <FlowLink />
                    )}
                    <ModuleCard
                        module={m}
                        fnId={fnId}
                        chainId={chainId}
                        modIdx={idx}
                        isDragging={isActiveDrag && dragPayload?.moduleId === m.id}
                        isSourceDuringDrag={isActiveDrag && isSameFn && dragPayload?.moduleId === m.id}
                        isInvalidDragTarget={isCrossFunction && isActiveDrag}
                        counter={counterMap.get(m.id)}
                        onDragStart={onDragStart}
                        onDragEnd={handleDragEnd}
                        onRename={name => onRenameModule(m.id, name)}
                        onOpenDrawer={() => onOpenDrawer(m.id)}
                        siblingNames={siblingNames.filter((_, i) => i !== idx)}
                    />
                </React.Fragment>
            ))}

            {modules.length === 0 && (
                <div className="fng-lane-track__empty">
                    empty chain — passthrough
                </div>
            )}

            {isActiveDrag && isSameFn && (
                <InsertSlot
                    idx={modules.length}
                    active={activeSlotIdx === modules.length}
                    hidden={hiddenSlots.has(modules.length)}
                />
            )}

            {!isActiveDrag && <FlowLink />}
            <Endpoint kind="out" />
            <AddModuleButton onClick={onAddModule} />
        </div>
    );
};
