import React, { useState } from 'react';
import type { Pipeline } from '../types';
import { Sparkline, formatPps, LaneStat, LaneCardActions, LaneCollapseButton } from '../../_shared/lane-editor';
import { EntityConfirmDialogs } from '@yanet/core/components';

interface PipelineCardHeaderProps {
    pipeline: Pipeline;
    isDirty: boolean;
    collapsed: boolean;
    totalPps: number;
    sparklineData: number[];
    onToggleCollapse: () => void;
    onOpenDiff: () => void;
    onDiscard: () => void;
    onDelete: () => void;
}

/**
 * Header row of a pipeline card: pipeline name (read-only), unsaved pill,
 * function count, pps stat, sparkline, and action buttons.
 */
export const PipelineCardHeader: React.FC<PipelineCardHeaderProps> = ({
    pipeline,
    isDirty,
    collapsed,
    totalPps,
    sparklineData,
    onToggleCollapse,
    onOpenDiff,
    onDiscard,
    onDelete,
}) => {
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [confirmDiscard, setConfirmDiscard] = useState(false);

    const totalFunctions = pipeline.functions.length;

    return (
        <div className="pl-card-header">
            <div className="pl-card-header__main-row">
                <LaneCollapseButton
                    prefix="pl"
                    collapsed={collapsed}
                    onToggle={onToggleCollapse}
                    expandLabel="Expand pipeline"
                    collapseLabel="Collapse pipeline"
                />

                <span className="pl-card-header__pipeline-id">{pipeline.id}</span>

                {isDirty && (
                    <span className="pl-card-header__unsaved-pill">unsaved</span>
                )}

                <div className="pl-card-header__spacer" />

                <div className="pl-card-header__stats">
                    <LaneStat prefix="pl" label="FUNCTIONS" value={totalFunctions} />
                    <div className="pl-card-header__stat-sep" />
                    <LaneStat prefix="pl" label="PPS" value={formatPps(totalPps)} accent />
                    <div className="pl-card-header__sparkline">
                        <Sparkline
                            data={sparklineData}
                            width={64}
                            height={22}
                            color="var(--pl-accent)"
                        />
                    </div>
                </div>

                <LaneCardActions
                    prefix="pl"
                    isDirty={isDirty}
                    saveDisabled={!isDirty}
                    deleteTitle="Delete pipeline"
                    deleteAriaLabel="Delete pipeline"
                    onDiscard={() => setConfirmDiscard(true)}
                    onOpenDiff={onOpenDiff}
                    onDelete={() => setConfirmDelete(true)}
                />
            </div>

            <EntityConfirmDialogs
                noun="pipeline"
                entityId={pipeline.id}
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
