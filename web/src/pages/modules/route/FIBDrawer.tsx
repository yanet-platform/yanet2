import React, { useEffect, useImperativeHandle, useState } from 'react';
import type { FIBRowItem, FIBRowErrors } from './types';
import { validateRow } from './validation';
import { DraftItemDrawer } from '../../_shared/draft';

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
            setErrors(validateRow(next));
            return next;
        });
    };

    const handleApply = (): void => {
        if (!draft) return;
        onChange(draft);
        onClose();
    };

    useImperativeHandle(ref, () => ({
        flushAndApply() {
            if (!open || !draft) return false;
            onChange(draft);
            return true;
        },
    }), [open, draft, onChange]);

    return (
        <DraftItemDrawer
            open={open}
            index={index}
            total={total}
            titleSingular="route"
            onClose={onClose}
            onApply={handleApply}
            onDelete={draft ? () => onDelete(draft) : undefined}
            onJump={onJump}
            ariaLabel="Edit route"
        >
            <section className="fw-section">
                <div className="fw-section-h">Destination</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">
                            Prefix <span className="fw-field__req">*</span>
                        </label>
                        <input
                            className={`fw-input fw-input--mono${errors.prefix ? ' fw-input--invalid' : ''}`}
                            value={draft?.prefix ?? ''}
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
                            value={draft?.dst_mac ?? ''}
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
                            value={draft?.src_mac ?? ''}
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
                            value={draft?.device ?? ''}
                            placeholder="eth0"
                            onChange={(e) => updateField('device', e.target.value.trim())}
                        />
                        {errors.device && (
                            <span className="fw-field__hint fw-field__error">{errors.device}</span>
                        )}
                    </div>
                </div>
            </section>
        </DraftItemDrawer>
    );
});

FIBDrawer.displayName = 'FIBDrawer';

export default FIBDrawer;
