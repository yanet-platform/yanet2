import React, { useEffect, useImperativeHandle, useState } from 'react';
import type { PrefixRowItem, PrefixRowErrors } from './types';
import { validateRow } from './validation';
import { DraftItemDrawer } from '../../_shared/draft';

interface PrefixDrawerProps {
    open: boolean;
    row: PrefixRowItem | null;
    index: number;
    total: number;
    onClose: () => void;
    /** Called when the user confirms the form. Updates local draft only — no API call. */
    onChange: (updated: PrefixRowItem) => void;
    onDelete: (row: PrefixRowItem) => void;
    onJump: (delta: number) => void;
}

export interface PrefixDrawerHandle {
    /** Flush any pending state and apply. Returns false if closed or invalid. */
    flushAndApply(): boolean;
}

/** Side drawer for adding/editing a single decap prefix row. */
const PrefixDrawer = React.forwardRef<PrefixDrawerHandle, PrefixDrawerProps>(({
    open,
    row,
    index,
    total,
    onClose,
    onChange,
    onDelete,
    onJump,
}, ref) => {
    const [draft, setDraft] = useState<PrefixRowItem | null>(null);
    const [errors, setErrors] = useState<PrefixRowErrors>({ prefix: null });

    useEffect(() => {
        if (open && row) {
            setDraft({ ...row });
            setErrors({ prefix: null });
        }
    }, [open, row?.id]);

    const updateField = <K extends keyof PrefixRowItem>(key: K, val: PrefixRowItem[K]): void => {
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
            titleSingular="prefix"
            onClose={onClose}
            onApply={handleApply}
            onDelete={draft ? () => onDelete(draft) : undefined}
            onJump={onJump}
            ariaLabel="Edit prefix"
        >
            <section className="fw-section">
                <div className="fw-section-h">Prefix</div>
                <div className="fw-section__body">
                    <div className="fw-field">
                        <label className="fw-field__label">
                            CIDR <span className="fw-field__req">*</span>
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
        </DraftItemDrawer>
    );
});

PrefixDrawer.displayName = 'PrefixDrawer';

export default PrefixDrawer;
