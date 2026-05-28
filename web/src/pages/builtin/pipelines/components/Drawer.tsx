import React, { useCallback, useEffect, useRef, useState } from 'react';
import type { FunctionRef } from '../types';
import { Sparkline, useSparklineHistory } from '../../_shared/lane-editor';
import { CloseIcon, TrashIcon } from '../../_shared/icons';
import { formatPps, formatBps } from '../../../../utils';
import { ConfirmDialog } from '../../../../components';
import type { InterpolatedCounterData } from '../../../../hooks';
import type { FunctionId } from '../../../../api/pipelines';

interface DrawerProps {
    ref_: FunctionRef;
    counter?: InterpolatedCounterData;
    loadFunctionList: () => Promise<FunctionId[]>;
    onClose: () => void;
    onChangeFunction: (name: string) => void;
    onRemove: () => void;
}

/**
 * Slide-in right-side inspector drawer for a function reference.
 * Shows a function picker, live counters, and a remove action.
 */
export const Drawer: React.FC<DrawerProps> = ({
    ref_,
    counter,
    loadFunctionList,
    onClose,
    onChangeFunction,
    onRemove,
}) => {
    const drawerRef = useRef<HTMLDivElement>(null);
    const [confirmRemove, setConfirmRemove] = useState(false);
    const [functionList, setFunctionList] = useState<FunctionId[]>([]);
    const [listLoading, setListLoading] = useState(true);

    const sparklineData = useSparklineHistory(ref_.id, counter?.pps ?? 0);
    const accent = 'var(--pl-accent)';

    useEffect(() => {
        const handleKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') {
                onClose();
            }
        };
        document.addEventListener('keydown', handleKey);
        return () => document.removeEventListener('keydown', handleKey);
    }, [onClose]);

    useEffect(() => {
        let cancelled = false;
        setListLoading(true);
        loadFunctionList().then(list => {
            if (!cancelled) {
                setFunctionList(list);
                setListLoading(false);
            }
        });
        return () => { cancelled = true; };
    }, [loadFunctionList]);

    const handleRemove = useCallback((): void => {
        onRemove();
        onClose();
    }, [onRemove, onClose]);

    return (
        <>
            <div className="pl-drawer__backdrop" onClick={onClose} />
            <div
                className="pl-drawer"
                ref={drawerRef}
                role="dialog"
                aria-label="Function reference inspector"
            >
                <div className="pl-drawer__header">
                    <div className="pl-drawer__title">
                        <div className="pl-drawer__subtitle">
                            FUNCTION REFERENCE
                        </div>
                        <div className="pl-drawer__name-edit">
                            {ref_.name || <span style={{ color: 'var(--pl-text-3)' }}>(unset)</span>}
                        </div>
                    </div>
                    <button
                        className="pl-drawer__close-btn"
                        onClick={onClose}
                        type="button"
                        aria-label="Close drawer"
                    >
                        <CloseIcon />
                    </button>
                </div>

                <div className="pl-drawer__section">
                    <div className="pl-drawer__section-label">Live counters</div>
                    <div className="pl-drawer__counters-grid">
                        <div className="pl-drawer__big-stat">
                            <div className="pl-drawer__big-stat-label">PPS</div>
                            <div
                                className="pl-drawer__big-stat-value"
                                style={{ color: accent }}
                            >
                                {counter ? formatPps(counter.pps) : '—'}
                            </div>
                        </div>
                        <div className="pl-drawer__big-stat">
                            <div className="pl-drawer__big-stat-label">BPS</div>
                            <div className="pl-drawer__big-stat-value">
                                {counter ? formatBps(counter.bps) : '—'}
                            </div>
                        </div>
                    </div>
                    {sparklineData.length >= 4 && (
                        <div>
                            <div className="pl-drawer__sparkline-label">pps · last {sparklineData.length} samples</div>
                            <div className="pl-drawer__sparkline">
                                <Sparkline
                                    data={sparklineData}
                                    width={364}
                                    height={48}
                                    color={accent}
                                />
                            </div>
                        </div>
                    )}
                </div>

                <div className="pl-drawer__section">
                    <div className="pl-drawer__section-label">Configuration</div>
                    <div className="pl-drawer__field">
                        <div className="pl-drawer__field-label">Function</div>
                        {listLoading ? (
                            <div className="pl-drawer__loading">Loading functions…</div>
                        ) : (
                            <select
                                className="pl-drawer__type-select"
                                value={ref_.name}
                                onChange={e => onChangeFunction(e.target.value)}
                            >
                                {!ref_.name && (
                                    <option value="">(unset)</option>
                                )}
                                {functionList.map(fn => (
                                    <option key={fn.name} value={fn.name ?? ''}>{fn.name}</option>
                                ))}
                            </select>
                        )}
                    </div>
                </div>

                <div className="pl-drawer__section">
                    <div className="pl-drawer__section-label">Actions</div>
                    <div className="pl-drawer__actions" style={{ padding: 0 }}>
                        <button
                            className="pl-drawer__action-btn pl-drawer__action-btn--danger"
                            type="button"
                            onClick={() => setConfirmRemove(true)}
                        >
                            <TrashIcon />
                            Remove from pipeline
                        </button>
                    </div>
                </div>

                <ConfirmDialog
                    open={confirmRemove}
                    onClose={() => setConfirmRemove(false)}
                    onConfirm={() => { setConfirmRemove(false); handleRemove(); }}
                    title="Remove function reference"
                    message={`Remove "${ref_.name || '(unset)'}" from the pipeline? This cannot be undone.`}
                    confirmText="Remove"
                    cancelText="Cancel"
                    danger
                />
            </div>
        </>
    );
};
