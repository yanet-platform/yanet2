import React, { useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { ForwardMode } from '../../../api/forward';
import { MODE_LABELS } from './types';
import type { RuleDraft, RuleItem } from './types';
import { emptyDraft } from './types';
import { itemToDraft, isValidCidr, isValidDeviceName } from './hooks';
import ChipInput from './ChipInput';
import type { ChipInputHandle } from './ChipInput';

interface RuleDrawerProps {
    open: boolean;
    mode: 'add' | 'edit';
    ruleItem: RuleItem | null;
    onClose: () => void;
    /** Called when the user confirms the rule form. Applies to local draft only — no API call. */
    onSave: (draft: RuleDraft) => void;
    onDelete: (item: RuleItem) => void;
    onDuplicate: (item: RuleItem) => void;
}

/** Imperative handle for flushing pending chip text and applying the drawer from outside. */
export interface RuleDrawerHandle {
    /**
     * Flush any pending chip input text into the draft and call onSave.
     * Returns false if the drawer is closed or the form is invalid.
     */
    flushAndApply(): boolean;
}

const MODES_ORDER: ForwardMode[] = [ForwardMode.NONE, ForwardMode.IN, ForwardMode.OUT];

/** Side drawer for adding/editing a forward rule. */
const RuleDrawer = React.forwardRef<RuleDrawerHandle, RuleDrawerProps>(({
    open,
    mode,
    ruleItem,
    onClose,
    onSave,
    onDelete,
    onDuplicate,
}, ref) => {
    const [draft, setDraft] = useState<RuleDraft>(emptyDraft());
    const [isDirty, setIsDirty] = useState(false);
    const initialDraftRef = useRef<RuleDraft | null>(null);
    const deviceNamesRef = useRef<ChipInputHandle>(null);
    const sourceCidrsRef = useRef<ChipInputHandle>(null);
    const dstCidrsRef = useRef<ChipInputHandle>(null);

    useEffect(() => {
        if (open) {
            // In 'edit' mode, pre-populate from the rule being edited.
            // In 'add' mode with a ruleItem, pre-populate for a duplicate workflow.
            const initial = ruleItem ? itemToDraft(ruleItem) : emptyDraft();
            initialDraftRef.current = initial;
            setDraft({ ...initial });
            setIsDirty(false);
        }
        // Intentionally exclude ruleItem object reference — we only re-initialize
        // when open or mode changes. The react-compiler handles memoization.
    }, [open, mode, ruleItem?.id]);

    const updateField = <K extends keyof RuleDraft>(key: K, val: RuleDraft[K]): void => {
        setDraft((prev) => ({ ...prev, [key]: val }));
        setIsDirty(true);
    };

    const isValid = draft.target.trim().length > 0;

    /**
     * Build the final draft by merging any pending chip text that has not yet
     * been committed via Enter/Tab. Called synchronously before onSave fires so
     * that text typed without pressing Enter is never lost.
     */
    const buildFlushedDraft = (base: RuleDraft): RuleDraft => ({
        ...base,
        deviceNames: [...base.deviceNames, ...(deviceNamesRef.current?.flush() ?? [])],
        sourceCidrs: [...base.sourceCidrs, ...(sourceCidrsRef.current?.flush() ?? [])],
        dstCidrs:    [...base.dstCidrs,    ...(dstCidrsRef.current?.flush()    ?? [])],
    });

    const handleApply = (): void => {
        const finalDraft = buildFlushedDraft(draft);
        setDraft(finalDraft);
        onSave(finalDraft);
    };

    useImperativeHandle(ref, () => ({
        flushAndApply() {
            if (!open || !isValid) return false;
            handleApply();
            return true;
        },
    }), [open, isValid, handleApply]);

    const handleClose = (): void => {
        if (isDirty) {
            const ok = window.confirm('You have unsaved changes. Close anyway?');
            if (!ok) return;
        }
        onClose();
    };

    const modeOptions = useMemo(() => MODES_ORDER.map((m) => ({
        value: m,
        label: MODE_LABELS[m],
        cls: m === ForwardMode.IN ? 'in' : m === ForwardMode.OUT ? 'out' : 'none',
    })), []);

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
                aria-label={mode === 'add' ? 'Add rule' : 'Edit rule'}
            >
                <header className="fw-drawer__head">
                    <h2 className="fw-drawer__title">
                        {mode === 'add' ? 'New rule' : (
                            <>Edit rule <span className="fw-drawer__rule-num">#{ruleItem?.index !== undefined ? ruleItem.index + 1 : ''}</span></>
                        )}
                    </h2>
                    <div className="fw-drawer__head-actions">
                        {mode === 'edit' && ruleItem && (
                            <>
                                <button
                                    type="button"
                                    className="fw-icon-btn"
                                    onClick={() => onDuplicate(ruleItem)}
                                    title="Duplicate rule"
                                >
                                    ⎘
                                </button>
                                <button
                                    type="button"
                                    className="fw-icon-btn fw-icon-btn--danger"
                                    onClick={() => onDelete(ruleItem)}
                                    title="Delete rule"
                                >
                                    🗑
                                </button>
                            </>
                        )}
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
                        <div className="fw-section-h">Identity</div>
                        <div className="fw-section__body">
                            <div className="fw-fgrid">
                                <div className="fw-field">
                                    <label className="fw-field__label">
                                        Target <span className="fw-field__req">*</span>
                                    </label>
                                    <input
                                        className="fw-input"
                                        placeholder="e.g. eth0"
                                        value={draft.target}
                                        onChange={(e) => updateField('target', e.target.value)}
                                    />
                                    <span className="fw-field__hint">Output target device matched traffic is forwarded to.</span>
                                </div>
                                <div className="fw-field">
                                    <label className="fw-field__label">Mode</label>
                                    <div className="fw-segmented" role="radiogroup" aria-label="Direction mode">
                                        {modeOptions.map((opt) => (
                                            <button
                                                key={opt.value}
                                                type="button"
                                                role="radio"
                                                aria-checked={draft.mode === opt.value}
                                                className={`fw-segmented__opt fw-segmented__opt--${opt.cls}${draft.mode === opt.value ? ' fw-segmented__opt--on' : ''}`}
                                                onClick={() => updateField('mode', opt.value)}
                                            >
                                                {opt.label}
                                            </button>
                                        ))}
                                    </div>
                                    <span className="fw-field__hint">
                                        {draft.mode === ForwardMode.IN && 'Match traffic entering the device.'}
                                        {draft.mode === ForwardMode.OUT && 'Match traffic exiting the device.'}
                                        {draft.mode === ForwardMode.NONE && 'Match without direction binding.'}
                                    </span>
                                </div>
                            </div>
                            <div className="fw-field">
                                <label className="fw-field__label">
                                    Counter <span className="fw-field__optional">optional</span>
                                </label>
                                <input
                                    className="fw-input"
                                    placeholder={draft.target ? `to_${draft.target}` : 'e.g. my_counter'}
                                    value={draft.counter}
                                    onChange={(e) => updateField('counter', e.target.value)}
                                />
                                <span className="fw-field__hint">Name shown in /stats. Leave empty to skip counting.</span>
                            </div>
                        </div>
                    </section>

                    <section className="fw-section">
                        <div className="fw-section-h">Match criteria</div>
                        <div className="fw-section__body">
                            <div className="fw-field">
                                <label className="fw-field__label">
                                    Devices
                                    <span className="fw-field__count">{draft.deviceNames.length || 'any'}</span>
                                </label>
                                <ChipInput
                                    ref={deviceNamesRef}
                                    value={draft.deviceNames}
                                    onChange={(v) => updateField('deviceNames', v)}
                                    placeholder="eth0, 0000:81:00.0…"
                                    kind="device"
                                    wildcardLabel="Any device"
                                    validator={isValidDeviceName}
                                />
                            </div>
                            <div className="fw-field">
                                <label className="fw-field__label">VLAN ranges</label>
                                <input
                                    className="fw-input fw-input--mono"
                                    placeholder="0-4095"
                                    value={draft.vlansRaw}
                                    onChange={(e) => updateField('vlansRaw', e.target.value)}
                                />
                                <span className="fw-field__hint">
                                    Single value <code>100</code>, range <code>100-200</code>, list <code>100, 200, 300-400</code>. Empty = all VLANs.
                                </span>
                            </div>
                            <div className="fw-fgrid">
                                <div className="fw-field">
                                    <label className="fw-field__label">
                                        Sources
                                        <span className="fw-field__count">{draft.sourceCidrs.length || 'any'}</span>
                                    </label>
                                    <ChipInput
                                        ref={sourceCidrsRef}
                                        value={draft.sourceCidrs}
                                        onChange={(v) => updateField('sourceCidrs', v)}
                                        placeholder="10.0.0.0/8…"
                                        kind="cidr"
                                        wildcardLabel="Any source"
                                        validator={isValidCidr}
                                    />
                                </div>
                                <div className="fw-field">
                                    <label className="fw-field__label">
                                        Destinations
                                        <span className="fw-field__count">{draft.dstCidrs.length || 'any'}</span>
                                    </label>
                                    <ChipInput
                                        ref={dstCidrsRef}
                                        value={draft.dstCidrs}
                                        onChange={(v) => updateField('dstCidrs', v)}
                                        placeholder="192.168.0.0/16…"
                                        kind="cidr"
                                        wildcardLabel="Any destination"
                                        validator={isValidCidr}
                                    />
                                </div>
                            </div>
                        </div>
                    </section>
                </div>

                <footer className="fw-drawer__foot">
                    <span className="fw-drawer__foot-meta">
                        {mode === 'add'
                            ? 'Will be appended to config.'
                            : `Rule #${(ruleItem?.index ?? -1) + 1}`}
                    </span>
                    <div className="fw-drawer__foot-actions">
                        <button type="button" className="fw-btn fw-btn--ghost" onClick={handleClose}>
                            Cancel
                        </button>
                        <button
                            type="button"
                            className="fw-btn fw-btn--primary"
                            disabled={!isValid}
                            onClick={handleApply}
                        >
                            Apply
                        </button>
                    </div>
                </footer>
            </aside>
        </>
    );
});

RuleDrawer.displayName = 'RuleDrawer';

export default RuleDrawer;
