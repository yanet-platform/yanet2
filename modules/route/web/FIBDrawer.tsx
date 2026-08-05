import React, { useEffect, useState } from 'react';
import type { FIBRowItem, FIBRowErrors } from './types';
import { validateRow } from './validation';
import { formatRangeInput, parseRangeInput } from './rangeInput';
import { DraftItemDrawer } from '@yanet/core/components/draft';
import { useRowDraft } from '@yanet/core/hooks';

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

const EMPTY_ERRORS: FIBRowErrors = { range: null, dst_mac: null, src_mac: null, device: null, counter: null };

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
    const { draft, errors, updateField, handleApply } = useRowDraft<FIBRowItem, FIBRowErrors>({
        open, row, emptyErrors: EMPTY_ERRORS, validateRow, onChange, onClose, handleRef: ref,
    });

    // The range field shows a single combined "CIDR or from - to" string,
    // decoupled from draft.from/to so an in-progress invalid edit doesn't
    // get silently dropped or reformatted mid-typing.
    const [rangeText, setRangeText] = useState('');
    useEffect(() => {
        if (open && row) {
            setRangeText(formatRangeInput(row.from, row.to));
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, row?.id]);

    const handleRangeChange = (text: string): void => {
        setRangeText(text);
        const parsed = parseRangeInput(text);
        if (parsed?.start && parsed?.end) {
            updateField('from', parsed.start);
            updateField('to', parsed.end);
        } else {
            updateField('from', text.trim());
            updateField('to', '');
        }
    };

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
            <section className="yn-section">
                <div className="yn-section-h">Destination</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Range <span className="yn-field__req">*</span>
                        </label>
                        <input
                            className={`yn-input yn-input--mono${errors.range ? ' yn-input--invalid' : ''}`}
                            value={rangeText}
                            placeholder="10.0.0.0/24 or 10.0.0.0 - 10.0.0.255"
                            onChange={(e) => handleRangeChange(e.target.value)}
                        />
                        {errors.range
                            ? <span className="yn-field__hint yn-field__error">{errors.range}</span>
                            : <span className="yn-field__hint">CIDR prefix or an explicit "from - to" range.</span>}
                    </div>
                </div>
            </section>

            <section className="yn-section">
                <div className="yn-section-h">L2 Rewrite</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Destination MAC <span className="yn-field__req">*</span>
                        </label>
                        <input
                            className={`yn-input yn-input--mono${errors.dst_mac ? ' yn-input--invalid' : ''}`}
                            value={draft?.dst_mac ?? ''}
                            placeholder="52:54:00:00:1c:57"
                            onChange={(e) => updateField('dst_mac', e.target.value.trim())}
                        />
                        {errors.dst_mac && (
                            <span className="yn-field__hint yn-field__error">{errors.dst_mac}</span>
                        )}
                    </div>
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Source MAC <span className="yn-field__req">*</span>
                        </label>
                        <input
                            className={`yn-input yn-input--mono${errors.src_mac ? ' yn-input--invalid' : ''}`}
                            value={draft?.src_mac ?? ''}
                            placeholder="52:54:00:12:34:56"
                            onChange={(e) => updateField('src_mac', e.target.value.trim())}
                        />
                        {errors.src_mac && (
                            <span className="yn-field__hint yn-field__error">{errors.src_mac}</span>
                        )}
                    </div>
                </div>
            </section>

            <section className="yn-section">
                <div className="yn-section-h">Egress</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Device <span className="yn-field__req">*</span>
                        </label>
                        <input
                            className={`yn-input${errors.device ? ' yn-input--invalid' : ''}`}
                            value={draft?.device ?? ''}
                            placeholder="eth0"
                            onChange={(e) => updateField('device', e.target.value.trim())}
                        />
                        {errors.device && (
                            <span className="yn-field__hint yn-field__error">{errors.device}</span>
                        )}
                    </div>
                    <div className="yn-field">
                        <label className="yn-field__label">Counter</label>
                        <input
                            className={`yn-input yn-input--mono${errors.counter ? ' yn-input--invalid' : ''}`}
                            value={draft?.counter ?? ''}
                            placeholder="nexthop_custom-name (leave empty to auto-generate)"
                            onChange={(e) => updateField('counter', e.target.value.trim())}
                        />
                        {errors.counter && (
                            <span className="yn-field__hint yn-field__error">{errors.counter}</span>
                        )}
                    </div>
                </div>
            </section>
        </DraftItemDrawer>
    );
});

FIBDrawer.displayName = 'FIBDrawer';

export default FIBDrawer;
