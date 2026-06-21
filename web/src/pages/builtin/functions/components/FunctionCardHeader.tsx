import React, { useMemo, useState } from 'react';
import type { NetworkFunction } from '../types';
import { metaFor } from '../moduleMeta';
import { Sparkline, formatPps, LaneStat, LaneCardActions, LaneCollapseButton } from '../../_shared/lane-editor';
import { EntityConfirmDialogs } from '@yanet/core/components';

interface FunctionCardHeaderProps {
    fn: NetworkFunction;
    isDirty: boolean;
    collapsed: boolean;
    hasErrors: boolean;
    totalPps: number;
    sparklineData: number[];
    onToggleCollapse: () => void;
    onOpenDiff: () => void;
    onDiscard: () => void;
    onDelete: () => void;
}

/**
 * Header row of a function card: type chip, function id, unsaved pill,
 * description, stats cluster, sparkline, action buttons.
 */
export const FunctionCardHeader: React.FC<FunctionCardHeaderProps> = ({
    fn,
    isDirty,
    collapsed,
    hasErrors,
    totalPps,
    sparklineData,
    onToggleCollapse,
    onOpenDiff,
    onDiscard,
    onDelete,
}) => {
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [confirmDiscard, setConfirmDiscard] = useState(false);

    const distinctTypes = useMemo(
        () => {
            const types = fn.chains.flatMap(c => c.modules.map(m => m.type)).filter(t => t !== '');
            return [...new Set(types)].sort();
        },
        [fn.chains],
    );

    const chipColor: string | undefined =
        distinctTypes.length === 1 ? metaFor(distinctTypes[0]).color : undefined;
    const sparklineColor = chipColor ?? 'var(--fn-text-3)';

    const totalChains = fn.chains.length;
    const totalModules = useMemo(
        () => fn.chains.reduce((s, c) => s + c.modules.length, 0),
        [fn.chains],
    );

    return (
        <div className="fn-card-header">
            <div className="fn-card-header__main-row">
                <LaneCollapseButton
                    prefix="fn"
                    collapsed={collapsed}
                    onToggle={onToggleCollapse}
                    expandLabel="Expand function"
                    collapseLabel="Collapse function"
                />

                {distinctTypes.length === 1 && (() => {
                    const meta = metaFor(distinctTypes[0]);
                    return (
                        <span
                            className="fn-card-header__type-chip"
                            style={{ background: `${meta.color}1f`, color: meta.color }}
                            title={meta.desc}
                        >
                            {distinctTypes[0]}
                        </span>
                    );
                })()}
                {distinctTypes.length >= 2 && (
                    <span
                        className="fn-card-header__type-chip"
                        style={{
                            background: 'color-mix(in srgb, var(--fn-text-3) 12%, transparent)',
                            color: 'var(--fn-text-3)',
                        }}
                        title={distinctTypes.join(', ')}
                    >
                        mixed
                    </span>
                )}

                <span className="fn-card-header__fn-id">{fn.id}</span>

                {isDirty && !hasErrors && (
                    <span className="fn-card-header__unsaved-pill">unsaved</span>
                )}
                {hasErrors && (
                    <span className="fn-card-header__error-pill">errors</span>
                )}

                {fn.description && (
                    <span className="fn-card-header__desc">{fn.description}</span>
                )}

                <div className="fn-card-header__spacer" />

                <div className="fn-card-header__stats">
                    <LaneStat prefix="fn" label="CHAINS" value={totalChains} />
                    <div className="fn-card-header__stat-sep" />
                    <LaneStat prefix="fn" label="MODULES" value={totalModules} />
                    <div className="fn-card-header__stat-sep" />
                    <LaneStat prefix="fn" label="PPS" value={formatPps(totalPps)} accent />
                    <div className="fn-card-header__sparkline">
                        <Sparkline
                            data={sparklineData}
                            width={64}
                            height={22}
                            color={sparklineColor}
                        />
                    </div>
                </div>

                <LaneCardActions
                    prefix="fn"
                    isDirty={isDirty}
                    saveDisabled={!isDirty || hasErrors}
                    deleteTitle="Delete function"
                    deleteAriaLabel="Delete function"
                    onDiscard={() => setConfirmDiscard(true)}
                    onOpenDiff={onOpenDiff}
                    onDelete={() => setConfirmDelete(true)}
                />
            </div>

            <EntityConfirmDialogs
                noun="function"
                entityId={fn.id}
                deleteOpen={confirmDelete}
                discardOpen={confirmDiscard}
                onDeleteClose={() => setConfirmDelete(false)}
                onDiscardClose={() => setConfirmDiscard(false)}
                onDelete={onDelete}
                onDiscard={onDiscard}
            />
        </div>
    );
};
