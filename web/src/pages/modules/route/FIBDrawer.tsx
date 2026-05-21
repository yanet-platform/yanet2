import React, { useEffect, useImperativeHandle, useState } from 'react';
import type { FIBRowItem, FIBRowErrors } from './types';
import { validateRow } from './validation';

interface FIBDrawerProps {
    open: boolean;
    row: FIBRowItem | null;
    index: number;
    total: number;
    onClose: () => void;
    /** Called when the user confirms the form. Updates local draft only — no API call. */
    onChange: (updated: FIBRowItem) => void;
    onDelete: (row: FIBRowItem) => void;
    onJump: (delta: number) => void;
}

export interface FIBDrawerHandle {
    /** Flush any pending state and apply. Returns false if closed or invalid. */
    flushAndApply(): boolean;
}

/** Side drawer for adding/editing a single FIB row. */
const FIBDrawer = React.forwardRef<FIBDrawerHandle, FIBDrawerProps>(({
    open,
    row,
    index,
    total,
    onClose,
    onChange,
    onDelete,
    onJump,
}, ref) => {
    const [draft, setDraft] = useState<FIBRowItem | null>(null);
    const [errors, setErrors] = useState<FIBRowErrors>({ prefix: null, dst_mac: null, src_mac: null, device: null });

    useEffect(() => {
        if (open && row) {
            setDraft({ ...row });
            setErrors({ prefix: null, dst_mac: null, src_mac: null, device: null });
        }
    }, [open, row?.id]);

    const updateField = <K extends keyof FIBRowItem>(key: K, val: FIBRowItem[K]): void => {
        setDraft((prev) => {
            if (!prev) return prev;
            const next = { ...prev, [key]: val };
            const errs = validateRow(next);
            setErrors(errs);
            return next;
        });
    };

    const handleApply = (): void => {
        if (!draft) return;
        onChange(draft);
    };

    useImperativeHandle(ref, () => ({
        flushAndApply() {
            if (!open || !draft) return false;
            handleApply();
            return true;
        },
    }), [open, draft]);

    const handleClose = (): void => {
        onClose();
    };

    return (
        <>
            <div
                className={`fw-backdrop${open ? ' fw-backdrop--open' : ''}`}
                onClick={handleClose}
                aria-hidden="true"
            />
            <aside
                className={`fw-drawer${open ? ' fw-drawer--open' : ''}`}
                role="dialog"
                aria-modal="true"
                aria-label="Edit route"
            >
                {draft && (
                    <>
                        <header className="fw-drawer__head">
                            <h2 className="fw-drawer__title">
                                Edit route{' '}
                                <span className="fw-drawer__rule-num">#{index + 1}</span>
                            </h2>
                            <div className="fw-drawer__head-actions">
                                <button
                                    type="button"
                                    className="fw-icon-btn"
                                    onClick={() => onJump(-1)}
                                    disabled={index === 0}
                                    title="Previous row (↑)"
                                >
                                    ↑
                                </button>
                                <button
                                    type="button"
                                    className="fw-icon-btn"
                                    onClick={() => onJump(1)}
                                    disabled={index === total - 1}
                                    title="Next row (↓)"
                                >
                                    ↓
                                </button>
                                <button
                                    type="button"
                                    className="fw-icon-btn fw-icon-btn--danger"
                                    onClick={() => { if (draft) onDelete(draft); }}
                                    title="Delete row"
                                >
                                    🗑
                                </button>
                                <button
                                    type="button"
                                    className="fw-icon-btn"
                                    onClick={handleClose}
                                    aria-label="Close drawer"
                                >
                                    ✕
                                </button>
                            </div>
                        </header>

                        <div className="fw-drawer__body">
                            <section className="fw-section">
                                <div className="fw-section-h">Destination</div>
                                <div className="fw-section__body">
                                    <div className="fw-field">
                                        <label className="fw-field__label">
                                            Prefix <span className="fw-field__req">*</span>
                                        </label>
                                        <input
                                            className={`fw-input fw-input--mono${errors.prefix ? ' fw-input--invalid' : ''}`}
                                            value={draft.prefix}
                                            placeholder="10.0.0.0/8 or 2a02:6b8::/32"
                                            onChange={(e) => updateField('prefix', e.target.value.trim())}
                                        />
                                        {errors.prefix
                                            ? <span className="fw-field__hint fw-field__error">{errors.prefix}</span>
                                            : <span className="fw-field__hint">IPv4 or IPv6 with mask.</span>
                                        }
                                    </div>
                                </div>
                            </section>

                            <section className="fw-section">
                                <div className="fw-section-h">L2 Rewrite</div>
                                <div className="fw-section__body">
                                    <div className="fw-field">
                                        <label className="fw-field__label">
                                            Destination MAC <span className="fw-field__req">*</span>
                                        </label>
                                        <input
                                            className={`fw-input fw-input--mono${errors.dst_mac ? ' fw-input--invalid' : ''}`}
                                            value={draft.dst_mac}
                                            placeholder="52:54:00:00:1c:57"
                                            onChange={(e) => updateField('dst_mac', e.target.value.trim())}
                                        />
                                        {errors.dst_mac && (
                                            <span className="fw-field__hint fw-field__error">{errors.dst_mac}</span>
                                        )}
                                    </div>
                                    <div className="fw-field">
                                        <label className="fw-field__label">
                                            Source MAC <span className="fw-field__req">*</span>
                                        </label>
                                        <input
                                            className={`fw-input fw-input--mono${errors.src_mac ? ' fw-input--invalid' : ''}`}
                                            value={draft.src_mac}
                                            placeholder="52:54:00:12:34:56"
                                            onChange={(e) => updateField('src_mac', e.target.value.trim())}
                                        />
                                        {errors.src_mac && (
                                            <span className="fw-field__hint fw-field__error">{errors.src_mac}</span>
                                        )}
                                    </div>
                                </div>
                            </section>

                            <section className="fw-section">
                                <div className="fw-section-h">Egress</div>
                                <div className="fw-section__body">
                                    <div className="fw-field">
                                        <label className="fw-field__label">
                                            Device <span className="fw-field__req">*</span>
                                        </label>
                                        <input
                                            className={`fw-input${errors.device ? ' fw-input--invalid' : ''}`}
                                            value={draft.device}
                                            placeholder="eth0"
                                            onChange={(e) => updateField('device', e.target.value.trim())}
                                        />
                                        {errors.device && (
                                            <span className="fw-field__hint fw-field__error">{errors.device}</span>
                                        )}
                                    </div>
                                </div>
                            </section>
                        </div>

                        <footer className="fw-drawer__foot">
                            <span className="fw-drawer__foot-meta">
                                Row <span className="fw-cell-mono fw-cell-strong">#{index + 1}</span> of {total}
                            </span>
                            <div className="fw-drawer__foot-actions">
                                <button type="button" className="fw-btn fw-btn--ghost" onClick={handleClose}>
                                    Cancel
                                </button>
                                <button
                                    type="button"
                                    className="fw-btn fw-btn--primary"
                                    onClick={handleApply}
                                >
                                    Apply
                                </button>
                            </div>
                        </footer>
                    </>
                )}
            </aside>
        </>
    );
});

FIBDrawer.displayName = 'FIBDrawer';

export default FIBDrawer;
