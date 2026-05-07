import React, { useMemo, useState } from 'react';
import type { NetworkFunction } from '../types';
import { metaFor } from '../moduleMeta';
import { Sparkline } from './Sparkline';
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

/** Save / floppy disk icon. */
const SaveIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 5h11l3 3v11H5zM8 5v5h7V5M8 14h8v5H8z" />
    </svg>
);

/** Trash / delete icon. */
const TrashIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 7h14M9 7V5h6v2M7 7l1 12h8l1-12" />
    </svg>
);

/** Discard / rollback icon (counter-clockwise rotate arrow). */
const DiscardIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M3 12a9 9 0 1 0 2.636-6.364L3 8" />
        <path d="M3 3v5h5" />
    </svg>
);

/** Chevron down icon. */
const ChevronDownIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M6 9l6 6 6-6" />
    </svg>
);

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
    <div className="fng-card-header__stat">
        <span
            className="fng-card-header__stat-value"
            style={accent ? { color: 'var(--fng-accent)' } : undefined}
        >
            {value}
        </span>
        <span className="fng-card-header__stat-label">{label}</span>
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
    const sparklineColor = chipColor ?? 'var(--fng-text-3)';

    const totalChains = fn.chains.length;
    const totalModules = useMemo(
        () => fn.chains.reduce((s, c) => s + c.modules.length, 0),
        [fn.chains],
    );

    return (
        <div className="fng-card-header">
            <div className="fng-card-header__main-row">
                <button
                    className="fng-card-header__collapse-btn"
                    onClick={onToggleCollapse}
                    type="button"
                    aria-expanded={!collapsed}
                    aria-label={collapsed ? 'Expand function' : 'Collapse function'}
                >
                    <span
                        className={`fng-card-header__chevron${collapsed ? '' : ' fng-card-header__chevron--open'}`}
                    >
                        <ChevronDownIcon />
                    </span>
                </button>

                {distinctTypes.length === 1 && (() => {
                    const meta = metaFor(distinctTypes[0]);
                    return (
                        <span
                            className="fng-card-header__type-chip"
                            style={{ background: `${meta.color}1f`, color: meta.color }}
                            title={meta.desc}
                        >
                            {distinctTypes[0]}
                        </span>
                    );
                })()}
                {distinctTypes.length >= 2 && (
                    <span
                        className="fng-card-header__type-chip"
                        style={{
                            background: 'color-mix(in srgb, var(--fng-text-3) 12%, transparent)',
                            color: 'var(--fng-text-3)',
                        }}
                        title={distinctTypes.join(', ')}
                    >
                        mixed
                    </span>
                )}

                <span className="fng-card-header__fn-id">{fn.id}</span>

                {isDirty && !hasErrors && (
                    <span className="fng-card-header__unsaved-pill">unsaved</span>
                )}
                {hasErrors && (
                    <span className="fng-card-header__error-pill">errors</span>
                )}

                {fn.description && (
                    <span className="fng-card-header__desc">{fn.description}</span>
                )}

                <div className="fng-card-header__spacer" />

                <div className="fng-card-header__stats">
                    <MiniStat label="CHAINS" value={totalChains} />
                    <div className="fng-card-header__stat-sep" />
                    <MiniStat label="MODULES" value={totalModules} />
                    <div className="fng-card-header__stat-sep" />
                    <MiniStat label="PPS" value={fmtPps(totalPps)} accent />
                    <div className="fng-card-header__sparkline">
                        <Sparkline
                            data={sparklineData}
                            width={64}
                            height={22}
                            color={sparklineColor}
                        />
                    </div>
                </div>

                <div className="fng-card-header__actions">
                    {isDirty && (
                        <button
                            className="fng-card-header__icon-btn fng-card-header__icon-btn--discard"
                            type="button"
                            title="Discard changes"
                            aria-label="Discard local changes"
                            onClick={() => setConfirmDiscard(true)}
                        >
                            <DiscardIcon />
                        </button>
                    )}
                    <button
                        className="fng-card-header__icon-btn fng-card-header__icon-btn--save"
                        onClick={onOpenDiff}
                        disabled={!isDirty || hasErrors}
                        type="button"
                        title={isDirty ? 'Review & apply' : 'No changes to save'}
                        aria-label="Review and apply changes"
                    >
                        <SaveIcon />
                    </button>
                    <button
                        className="fng-card-header__icon-btn fng-card-header__icon-btn--delete"
                        onClick={() => setConfirmDelete(true)}
                        type="button"
                        title="Delete function"
                        aria-label="Delete function"
                    >
                        <TrashIcon />
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
