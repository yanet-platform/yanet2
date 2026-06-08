import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useDrawerFlush } from '../../../hooks';
import { ForwardMode } from '../../../api/forward';
import { MODE_LABELS } from './types';
import type { RuleDraft, RuleItem } from './types';
import { emptyDraft } from './types';
import { itemToDraft } from './hooks';
import { isValidCidr, isValidDeviceName } from '../../../utils';
import ChipInput from './ChipInput';
import type { ChipInputHandle } from './ChipInput';
import { DrawerShell } from '../../../components';

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

    const { handleApply } = useDrawerFlush({
        draft,
        setDraft,
        onSave,
        refs: { deviceNames: deviceNamesRef, sourceCidrs: sourceCidrsRef, dstCidrs: dstCidrsRef },
        handleRef: ref,
        open,
        canApply: isValid,
    });

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
        <DrawerShell
            open={open}
            ariaLabel={mode === 'add' ? 'Add rule' : 'Edit rule'}
            onBackdropClick={handleClose}
            title={mode === 'add' ? 'New rule' : (
                <>Edit rule <span className="yn-drawer__rule-num">#{ruleItem?.index !== undefined ? ruleItem.index + 1 : ''}</span></>
            )}
            headActions={<>
                {mode === 'edit' && ruleItem && (
                    <>
                        <button
                            type="button"
                            className="yn-icon-btn"
                            onClick={() => onDuplicate(ruleItem)}
                            title="Duplicate rule"
                        >
                            ⎘
                        </button>
                        <button
                            type="button"
                            className="yn-icon-btn yn-icon-btn--danger"
                            onClick={() => onDelete(ruleItem)}
                            title="Delete rule"
                        >
                            🗑
                        </button>
                    </>
                )}
                <button
                    type="button"
                    className="yn-icon-btn"
                    onClick={handleClose}
                    aria-label="Close drawer"
                >
                    ✕
                </button>
            </>}
            footMeta={mode === 'add'
                ? 'Will be appended to config.'
                : `Rule #${(ruleItem?.index ?? -1) + 1}`}
            footActions={<>
                <button type="button" className="yn-btn yn-btn--ghost" onClick={handleClose}>
                    Cancel
                </button>
                <button
                    type="button"
                    className="yn-btn yn-btn--primary"
                    disabled={!isValid}
                    onClick={handleApply}
                >
                    Apply
                </button>
            </>}
        >
            <section className="yn-section">
                <div className="yn-section-h">Identity</div>
                <div className="yn-section__body">
                    <div className="yn-fgrid">
                        <div className="yn-field">
                            <label className="yn-field__label">
                                Target <span className="yn-field__req">*</span>
                            </label>
                            <input
                                className="yn-input"
                                placeholder="e.g. eth0"
                                value={draft.target}
                                onChange={(e) => updateField('target', e.target.value)}
                            />
                            <span className="yn-field__hint">Output target device matched traffic is forwarded to.</span>
                        </div>
                        <div className="yn-field">
                            <label className="yn-field__label">Mode</label>
                            <div className="yn-segmented" role="radiogroup" aria-label="Direction mode">
                                {modeOptions.map((opt) => (
                                    <button
                                        key={opt.value}
                                        type="button"
                                        role="radio"
                                        aria-checked={draft.mode === opt.value}
                                        className={`yn-segmented__opt yn-segmented__opt--${opt.cls}${draft.mode === opt.value ? ' yn-segmented__opt--on' : ''}`}
                                        onClick={() => updateField('mode', opt.value)}
                                    >
                                        {opt.label}
                                    </button>
                                ))}
                            </div>
                            <span className="yn-field__hint">
                                {draft.mode === ForwardMode.IN && 'Match traffic entering the device.'}
                                {draft.mode === ForwardMode.OUT && 'Match traffic exiting the device.'}
                                {draft.mode === ForwardMode.NONE && 'Match without direction binding.'}
                            </span>
                        </div>
                    </div>
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Counter <span className="yn-field__optional">optional</span>
                        </label>
                        <input
                            className="yn-input"
                            placeholder={draft.target ? `to_${draft.target}` : 'e.g. my_counter'}
                            value={draft.counter}
                            onChange={(e) => updateField('counter', e.target.value)}
                        />
                        <span className="yn-field__hint">Name shown in /stats. Leave empty to skip counting.</span>
                    </div>
                </div>
            </section>

            <section className="yn-section">
                <div className="yn-section-h">Match criteria</div>
                <div className="yn-section__body">
                    <div className="yn-field">
                        <label className="yn-field__label">
                            Devices
                            <span className="yn-field__count">{draft.deviceNames.length || 'any'}</span>
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
                    <div className="yn-field">
                        <label className="yn-field__label">VLAN ranges</label>
                        <input
                            className="yn-input yn-input--mono"
                            placeholder="0-4095"
                            value={draft.vlansRaw}
                            onChange={(e) => updateField('vlansRaw', e.target.value)}
                        />
                        <span className="yn-field__hint">
                            Single value <code>100</code>, range <code>100-200</code>, list <code>100, 200, 300-400</code>. Empty = all VLANs.
                        </span>
                    </div>
                    <div className="yn-fgrid">
                        <div className="yn-field">
                            <label className="yn-field__label">
                                Sources
                                <span className="yn-field__count">{draft.sourceCidrs.length || 'any'}</span>
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
                        <div className="yn-field">
                            <label className="yn-field__label">
                                Destinations
                                <span className="yn-field__count">{draft.dstCidrs.length || 'any'}</span>
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
        </DrawerShell>
    );
});

RuleDrawer.displayName = 'RuleDrawer';

export default RuleDrawer;
