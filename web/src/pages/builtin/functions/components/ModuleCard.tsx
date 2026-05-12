import React, { memo, useCallback } from 'react';
import type { Module, DragPayload } from '../types';
import { metaFor } from '../moduleMeta';
import { InlineEdit } from './InlineEdit';
import { Sparkline, useSparklineHistory } from '../../_shared/lane-editor';
import { formatPps } from '../../../../utils';
import type { InterpolatedCounterData } from '../../../../hooks';

interface ModuleCardProps {
    module: Module;
    fnId: string;
    chainId: string;
    modIdx: number;
    isDragging: boolean;
    /** True when this card is the source being dragged in the same chain. */
    isSourceDuringDrag?: boolean;
    isInvalidDragTarget: boolean;
    counter?: InterpolatedCounterData;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onRename: (newName: string) => void;
    onOpenDrawer: () => void;
}

const validateModuleName = (name: string): string | null => {
    if (!name || name.trim() === '') {
        return 'Name cannot be empty';
    }
    return null;
};

/** Small kebab / more-options icon. */
const KebabIcon = (): React.JSX.Element => (
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
        <circle cx="12" cy="5" r="1" fill="currentColor" />
        <circle cx="12" cy="12" r="1" fill="currentColor" />
        <circle cx="12" cy="19" r="1" fill="currentColor" />
    </svg>
);

/**
 * A single module card rendered inside a lane track.
 * Draggable (native HTML5), inline-editable name, opens drawer on click.
 * Layout: 3px left accent bar, row 1 (type chip + name + kebab),
 * row 2 (sparkline + pps number).
 */
export const ModuleCard: React.FC<ModuleCardProps> = memo(({
    module,
    fnId,
    chainId,
    modIdx,
    isDragging,
    isSourceDuringDrag,
    isInvalidDragTarget,
    counter,
    onDragStart,
    onDragEnd,
    onRename,
    onOpenDrawer,
}) => {
    const meta = metaFor(module.type);
    const sparklineData = useSparklineHistory(module.id, counter?.pps ?? 0);

    const handleDragStart = useCallback((e: React.DragEvent<HTMLDivElement>): void => {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', module.id);
        onDragStart({ fromFnId: fnId, fromChainId: chainId, fromModIdx: modIdx, moduleId: module.id });
    }, [fnId, chainId, modIdx, module.id, onDragStart]);

    const validate = useCallback((name: string): string | null => {
        return validateModuleName(name);
    }, []);

    const borderColor = isInvalidDragTarget
        ? 'var(--fn-danger)'
        : 'var(--fn-line)';

    return (
        <div
            className={[
                'fn-module-card',
                isDragging ? 'fn-module-card--dragging' : '',
                isSourceDuringDrag ? 'fn-module-card--drag-source' : '',
                isInvalidDragTarget ? 'fn-module-card--invalid-target' : '',
            ].filter(Boolean).join(' ')}
            style={{ borderColor }}
            draggable
            onDragStart={handleDragStart}
            onDragEnd={onDragEnd}
            onClick={onOpenDrawer}
            title={isSourceDuringDrag ? 'Drop here to cancel' : undefined}
        >
            <div
                className="fn-module-card__accent-bar"
                style={{ background: meta.color }}
            />
            <div className="fn-module-card__content">
                <div className="fn-module-card__top-row">
                    <span
                        className="fn-module-card__type-chip"
                        style={{
                            background: `${meta.color}1f`,
                            color: meta.color,
                        }}
                        title={meta.desc}
                    >
                        {module.type}
                    </span>
                    <div className="fn-module-card__name">
                        <InlineEdit
                            value={module.name}
                            onChange={onRename}
                            validate={validate}
                            placeholder="name"
                            hintVariant="module"
                        />
                    </div>
                    <button
                        className="fn-module-card__kebab"
                        onClick={e => { e.stopPropagation(); onOpenDrawer(); }}
                        type="button"
                        title="Module options"
                        aria-label="Module options"
                    >
                        <KebabIcon />
                    </button>
                </div>
                <div className="fn-module-card__sparkline-row">
                    <Sparkline
                        data={sparklineData}
                        width={120}
                        height={15}
                        color={meta.color}
                    />
                    <span className="fn-module-card__counter">
                        {counter ? formatPps(counter.pps) : '— pps'}{' '}
                        <span className="fn-module-card__counter-unit"></span>
                    </span>
                </div>
            </div>
        </div>
    );
});

ModuleCard.displayName = 'ModuleCard';
