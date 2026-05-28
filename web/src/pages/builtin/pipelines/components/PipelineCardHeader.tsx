import React, { useState } from 'react';
import type { Pipeline } from '../types';
import { Sparkline } from '../../_shared/lane-editor';
import { TrashIcon, SaveIcon, DiscardIcon, ChevronDownIcon } from '../../_shared/icons';
import { ConfirmDialog } from '../../../../components';

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

/** Format a pps number with K/M suffix. */
const fmtPps = (v: number): string => {
    if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`;
    if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`;
    return String(Math.round(v));
};

interface MiniStatProps {
    label: string;
    value: string | number;
    accent?: boolean;
}

const MiniStat = ({ label, value, accent }: MiniStatProps): React.JSX.Element => (
    <div className="pl-card-header__stat">
        <span
            className="pl-card-header__stat-value"
            style={accent ? { color: 'var(--pl-accent)' } : undefined}
        >
            {value}
        </span>
        <span className="pl-card-header__stat-label">{label}</span>
    </div>
);

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
                <button
                    className="pl-card-header__collapse-btn"
                    onClick={onToggleCollapse}
                    type="button"
                    aria-expanded={!collapsed}
                    aria-label={collapsed ? 'Expand pipeline' : 'Collapse pipeline'}
                >
                    <span
                        className={`pl-card-header__chevron${collapsed ? '' : ' pl-card-header__chevron--open'}`}
                    >
                        <ChevronDownIcon />
                    </span>
                </button>

                <span className="pl-card-header__pipeline-id">{pipeline.id}</span>

                {isDirty && (
                    <span className="pl-card-header__unsaved-pill">unsaved</span>
                )}

                <div className="pl-card-header__spacer" />

                <div className="pl-card-header__stats">
                    <MiniStat label="FUNCTIONS" value={totalFunctions} />
                    <div className="pl-card-header__stat-sep" />
                    <MiniStat label="PPS" value={fmtPps(totalPps)} accent />
                    <div className="pl-card-header__sparkline">
                        <Sparkline
                            data={sparklineData}
                            width={64}
                            height={22}
                            color="var(--pl-accent)"
                        />
                    </div>
                </div>

                <div className="pl-card-header__actions">
                    {isDirty && (
                        <button
                            className="pl-card-header__icon-btn pl-card-header__icon-btn--discard"
                            type="button"
                            title="Discard changes"
                            aria-label="Discard local changes"
                            onClick={() => setConfirmDiscard(true)}
                        >
                            <DiscardIcon />
                        </button>
                    )}
                    <button
                        className="pl-card-header__icon-btn pl-card-header__icon-btn--save"
                        onClick={onOpenDiff}
                        disabled={!isDirty}
                        type="button"
                        title={isDirty ? 'Review & apply' : 'No changes to save'}
                        aria-label="Review and apply changes"
                    >
                        <SaveIcon />
                    </button>
                    <button
                        className="pl-card-header__icon-btn pl-card-header__icon-btn--delete"
                        onClick={() => setConfirmDelete(true)}
                        type="button"
                        title="Delete pipeline"
                        aria-label="Delete pipeline"
                    >
                        <TrashIcon size={18} />
                    </button>
                </div>
            </div>

            <ConfirmDialog
                open={confirmDelete}
                onClose={() => setConfirmDelete(false)}
                onConfirm={() => { setConfirmDelete(false); onDelete(); }}
                title="Delete pipeline"
                message={`Delete pipeline "${pipeline.id}"? This cannot be undone.`}
                confirmText="Delete"
                cancelText="Cancel"
                danger
            />

            <ConfirmDialog
                open={confirmDiscard}
                onClose={() => setConfirmDiscard(false)}
                onConfirm={() => { setConfirmDiscard(false); onDiscard(); }}
                title={`Discard changes to "${pipeline.id}"?`}
                message="All local edits to this pipeline will be discarded."
                confirmText="Discard"
                cancelText="Cancel"
                danger
            />
        </div>
    );
};
