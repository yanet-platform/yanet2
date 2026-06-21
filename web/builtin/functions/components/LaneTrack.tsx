import React from 'react';
import type { Module, DragPayload } from '../types';
import { getDragPayload, InsertSlot, Endpoint, FlowLink, AddSlotButton } from '../../_shared/lane-editor';
import { ModuleCard } from './ModuleCard';
import type { InterpolatedCounterData } from '@yanet/core/hooks';
import { useLaneTrackDnD } from '@yanet/core/hooks';

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
 * Handles dragover slot detection via DOM geometry.
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
    const { isDragging, dragPayload } = dragState;
    const isActiveDrag = isDragging && !!dragPayload;
    const isFunctionDrag = isActiveDrag && dragPayload.fromChainId !== dragPayload.fromFnId;
    const isSameOwner = isFunctionDrag && dragPayload.fromFnId === fnId;
    const isSameContainer = isSameOwner && dragPayload.fromChainId === chainId;

    const fromModIdx = isSameContainer
        ? dragPayload.fromModIdx
        : -1;

    const hiddenSlots = new Set<number>();
    if (fromModIdx >= 0) {
        hiddenSlots.add(fromModIdx);
        hiddenSlots.add(fromModIdx + 1);
    }

    const { activeSlotIdx, containerRef, handleDragOver, handleDragLeave, handleDrop, handleDragEnd } =
        useLaneTrackDnD<DragPayload>({
            isItemDrag: isFunctionDrag,
            isActiveDrag,
            onDragEnd,
            getPayload: getDragPayload,
            acceptPayload: (p) => p.fromChainId !== p.fromFnId,
            sameContainerSrcIdx: (p) => (p.fromFnId === fnId && p.fromChainId === chainId) ? p.fromModIdx : -1,
            onDropAt: (toIdx) => onDrop(chainId, toIdx),
        });

    return (
        <div
            className="fn-lane-track"
            ref={containerRef}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
        >
            <Endpoint kind="in" />

            {modules.map((m, idx) => (
                <React.Fragment key={m.id}>
                    {isFunctionDrag ? (
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
                        isDragging={isFunctionDrag && dragPayload?.moduleId === m.id}
                        isSourceDuringDrag={isFunctionDrag && isSameOwner && dragPayload?.moduleId === m.id}
                        isInvalidDragTarget={false}
                        counter={counterMap.get(m.id)}
                        onDragStart={onDragStart}
                        onDragEnd={handleDragEnd}
                        onRename={name => onRenameModule(m.id, name)}
                        onOpenDrawer={() => onOpenDrawer(m.id)}
                    />
                </React.Fragment>
            ))}

            {modules.length === 0 && (
                <>
                    <FlowLink />
                    <div className="fn-lane-track__empty">
                        passthrough
                    </div>
                </>
            )}

            {isFunctionDrag && (
                <InsertSlot
                    idx={modules.length}
                    active={activeSlotIdx === modules.length}
                    hidden={hiddenSlots.has(modules.length)}
                />
            )}

            {!isActiveDrag && <FlowLink />}
            <Endpoint kind="out" />
            <AddSlotButton onClick={onAddModule} className="fn-add-module-btn" label="Add module" />
        </div>
    );
};
