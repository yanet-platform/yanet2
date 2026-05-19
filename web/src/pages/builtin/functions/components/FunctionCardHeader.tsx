import React, { useMemo, useState } from 'react';
import type { NetworkFunction } from '../types';
import { metaFor } from '../moduleMeta';
import { Sparkline } from '../../_shared/lane-editor';
import { TrashIcon, SaveIcon, DiscardIcon, ChevronDownIcon } from '../../_shared/icons';
import { ConfirmDialog } from '../../../../components';

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

/** Stat cell: value on top, label on bottom, right-aligned. */
const MiniStat = ({ label, value, accent }: MiniStatProps): React.JSX.Element => (
    <div className="fn-card-header__stat">
        <span
            className="fn-card-header__stat-value"
            style={accent ? { color: 'var(--fn-accent)' } : undefined}
        >
            {value}
        </span>
        <span className="fn-card-header__stat-label">{label}</span>
    </div>
);

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
                <button
                    className="fn-card-header__collapse-btn"
                    onClick={onToggleCollapse}
                    type="button"
                    aria-expanded={!collapsed}
                    aria-label={collapsed ? 'Expand function' : 'Collapse function'}
                >
                    <span
                        className={`fn-card-header__chevron${collapsed ? '' : ' fn-card-header__chevron--open'}`}
                    >
                        <ChevronDownIcon />
                    </span>
                </button>

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
                    <MiniStat label="CHAINS" value={totalChains} />
                    <div className="fn-card-header__stat-sep" />
                    <MiniStat label="MODULES" value={totalModules} />
                    <div className="fn-card-header__stat-sep" />
                    <MiniStat label="PPS" value={fmtPps(totalPps)} accent />
                    <div className="fn-card-header__sparkline">
                        <Sparkline
                            data={sparklineData}
                            width={64}
                            height={22}
                            color={sparklineColor}
                        />
                    </div>
                </div>

                <div className="fn-card-header__actions">
                    {isDirty && (
                        <button
                            className="fn-card-header__icon-btn fn-card-header__icon-btn--discard"
                            type="button"
                            title="Discard changes"
                            aria-label="Discard local changes"
                            onClick={() => setConfirmDiscard(true)}
                        >
                            <DiscardIcon />
                        </button>
                    )}
                    <button
                        className="fn-card-header__icon-btn fn-card-header__icon-btn--save"
                        onClick={onOpenDiff}
                        disabled={!isDirty || hasErrors}
                        type="button"
                        title={isDirty ? 'Review & apply' : 'No changes to save'}
                        aria-label="Review and apply changes"
                    >
                        <SaveIcon />
                    </button>
                    <button
                        className="fn-card-header__icon-btn fn-card-header__icon-btn--delete"
                        onClick={() => setConfirmDelete(true)}
                        type="button"
                        title="Delete function"
                        aria-label="Delete function"
                    >
                        <TrashIcon size={18} />
                    </button>
                </div>
            </div>

            <ConfirmDialog
                open={confirmDelete}
                onClose={() => setConfirmDelete(false)}
                onConfirm={() => { setConfirmDelete(false); onDelete(); }}
                title="Delete function"
                message={`Delete function "${fn.id}"? This cannot be undone.`}
                confirmText="Delete"
                cancelText="Cancel"
                danger
            />

            <ConfirmDialog
                open={confirmDiscard}
                onClose={() => setConfirmDiscard(false)}
                onConfirm={() => { setConfirmDiscard(false); onDiscard(); }}
                title={`Discard changes to "${fn.id}"?`}
                message="All local edits to this function will be discarded."
                confirmText="Discard"
                cancelText="Cancel"
                danger
            />
        </div>
    );
};
