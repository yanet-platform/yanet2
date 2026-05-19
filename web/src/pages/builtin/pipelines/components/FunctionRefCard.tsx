import React, { memo, useCallback } from 'react';
import type { FunctionRef, DragPayload } from '../types';
import { Sparkline, useSparklineHistory } from '../../_shared/lane-editor';
import { TrashIcon } from '../../_shared/icons';
import { formatPps } from '../../../../utils';
import type { InterpolatedCounterData } from '../../../../hooks';

/** Small pencil icon. */
const PencilIcon = (): React.JSX.Element => (
    <svg
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
    >
        <path d="M15.232 5.232l3.536 3.536M9 13l-4 4 4.5.5.5-4.5zM16.5 3.5a2.121 2.121 0 0 1 3 3L8 18l-4 1 1-4L16.5 3.5z" />
    </svg>
);

interface FunctionRefCardProps {
    ref_: FunctionRef;
    pipelineId: string;
    refIdx: number;
    isDragging: boolean;
    isSourceDuringDrag?: boolean;
    isInvalidDragTarget: boolean;
    counter?: InterpolatedCounterData;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onOpenDrawer: () => void;
    onRemove: () => void;
}

/**
 * A single function-reference card rendered inside a pipeline track.
 * Draggable (native HTML5), opens drawer on click, trash button removes.
 * Layout: 3px left accent bar, row 1 (fn name + pencil + trash),
 * row 2 (sparkline + pps number).
 */
export const FunctionRefCard: React.FC<FunctionRefCardProps> = memo(({
    ref_,
    pipelineId,
    refIdx,
    isDragging,
    isSourceDuringDrag,
    isInvalidDragTarget,
    counter,
    onDragStart,
    onDragEnd,
    onOpenDrawer,
    onRemove,
}) => {
    const sparklineData = useSparklineHistory(ref_.id, counter?.pps ?? 0);

    const handleDragStart = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', ref_.id);
        onDragStart({ fromFnId: pipelineId, fromChainId: pipelineId, fromModIdx: refIdx, moduleId: ref_.id });
    }, [pipelineId, refIdx, ref_.id, onDragStart]);

    const accent = 'var(--pl-accent)';
    const isEmpty = !ref_.name;

    return (
        <div
            className={[
                'pl-ref-card',
                isDragging ? 'pl-ref-card--dragging' : '',
                isSourceDuringDrag ? 'pl-ref-card--drag-source' : '',
                isInvalidDragTarget ? 'pl-ref-card--invalid-target' : '',
            ].filter(Boolean).join(' ')}
            draggable
            onDragStart={handleDragStart}
            onDragEnd={onDragEnd}
            onClick={onOpenDrawer}
            title={isSourceDuringDrag ? 'Drop here to cancel' : undefined}
        >
            <div
                className="pl-ref-card__accent-bar"
                style={{ background: accent }}
            />
            <div className="pl-ref-card__content">
                <div className="pl-ref-card__top-row">
                    <span className={`pl-ref-card__name${isEmpty ? ' pl-ref-card__name--empty' : ''}`}>
                        {isEmpty ? '(unset)' : ref_.name}
                    </span>
                    <button
                        className="pl-ref-card__icon-btn"
                        onClick={e => { e.stopPropagation(); onOpenDrawer(); }}
                        type="button"
                        title="Edit function reference"
                        aria-label="Edit function reference"
                    >
                        <PencilIcon />
                    </button>
                    <button
                        className="pl-ref-card__icon-btn pl-ref-card__icon-btn--danger"
                        onClick={e => { e.stopPropagation(); onRemove(); }}
                        type="button"
                        title="Remove function reference"
                        aria-label="Remove function reference"
                    >
                        <TrashIcon size={12} />
                    </button>
                </div>
                <div className="pl-ref-card__sparkline-row">
                    <Sparkline
                        data={sparklineData}
                        width={120}
                        height={15}
                        color={accent}
                    />
                    <span className="pl-ref-card__counter">
                        {counter ? formatPps(counter.pps) : '— pps'}{' '}
                        <span className="pl-ref-card__counter-unit"></span>
                    </span>
                </div>
            </div>
        </div>
    );
});

FunctionRefCard.displayName = 'FunctionRefCard';
