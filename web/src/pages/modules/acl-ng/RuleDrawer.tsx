import React, { useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';
import { ActionKind, ACTION_KIND_LABELS } from '../../../api/acl-ng';
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
    onSave: (draft: RuleDraft) => void;
    onDelete: (item: RuleItem) => void;
    onDuplicate: (item: RuleItem) => void;
}

/** Imperative handle for flushing pending chip text and applying the drawer from outside. */
export interface RuleDrawerHandle {
    flushAndApply(): boolean;
}

const ALL_KINDS: ActionKind[] = [
    ActionKind.ACTION_KIND_CREATE_STATE,
    ActionKind.ACTION_KIND_CHECK_STATE,
    ActionKind.ACTION_KIND_COUNT,
    ActionKind.ACTION_KIND_LOG,
    ActionKind.ACTION_KIND_PASS,
    ActionKind.ACTION_KIND_DENY,
];

const TERMINAL_KINDS = new Set<ActionKind>([
    ActionKind.ACTION_KIND_PASS,
    ActionKind.ACTION_KIND_DENY,
]);

/** Side drawer for adding/editing an ACL NG rule. */
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
    const deviceNamesRef = useRef<ChipInputHandle>(null);
    const sourceCidrsRef = useRef<ChipInputHandle>(null);
    const dstCidrsRef = useRef<ChipInputHandle>(null);

    useEffect(() => {
        if (open) {
            const initial = ruleItem ? itemToDraft(ruleItem) : emptyDraft();
            setDraft({ ...initial });
            setIsDirty(false);
        }
    }, [open, mode, ruleItem?.id]);

    const updateField = <K extends keyof RuleDraft>(key: K, val: RuleDraft[K]): void => {
        setDraft(prev => ({ ...prev, [key]: val }));
        setIsDirty(true);
    };

    const buildFlushedDraft = (base: RuleDraft): RuleDraft => ({
        ...base,
        deviceNames: [...base.deviceNames, ...(deviceNamesRef.current?.flush() ?? [])],
        sourceCidrs: [...base.sourceCidrs, ...(sourceCidrsRef.current?.flush() ?? [])],
        dstCidrs: [...base.dstCidrs, ...(dstCidrsRef.current?.flush() ?? [])],
    });

    const handleApply = useCallback((): void => {
        const finalDraft = buildFlushedDraft(draft);
        setDraft(finalDraft);
        onSave(finalDraft);
    }, [draft, onSave]);

    useImperativeHandle(ref, () => ({
        flushAndApply() {
            if (!open) return false;
            handleApply();
            return true;
        },
    }), [open, handleApply]);

    const handleClose = (): void => {
        if (isDirty) {
            const ok = window.confirm('You have unsaved changes. Close anyway?');
            if (!ok) return;
        }
        onClose();
    };

    const addAction = (kind: ActionKind): void => {
        updateField('actions', [...draft.actions, kind]);
    };

    const removeAction = (idx: number): void => {
        updateField('actions', draft.actions.filter((_, i) => i !== idx));
    };

    const changeAction = (idx: number, kind: ActionKind): void => {
        const next = [...draft.actions];
        next[idx] = kind;
        updateField('actions', next);
    };

    const terminalIdx = draft.actions.findIndex(k => TERMINAL_KINDS.has(k));
    const hasNoTerminal = draft.actions.length > 0 && terminalIdx === -1;
    const hasUnreachable = terminalIdx !== -1 && terminalIdx < draft.actions.length - 1;

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
                aria-label={mode === 'add' ? 'Add ACL rule' : 'Edit ACL rule'}
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
                        <div className="fw-section-h">Actions</div>
                        <div className="fw-section__body">
                            <div className="fw-field">
                                <label className="fw-field__label">
                                    Action chain
                                    <span className="fw-field__count">{draft.actions.length} step{draft.actions.length !== 1 ? 's' : ''}</span>
                                </label>
                                <div className="acl-action-editor">
                                    {draft.actions.map((kind, idx) => (
                                        <div key={idx} className="acl-action-row">
                                            <span className="acl-action-row__idx">{idx + 1}.</span>
                                            <select
                                                className="fw-input acl-action-select"
                                                value={kind}
                                                onChange={e => changeAction(idx, parseInt(e.target.value, 10) as ActionKind)}
                                            >
                                                {ALL_KINDS.map(k => (
                                                    <option key={k} value={k}>
                                                        {ACTION_KIND_LABELS[k]}
                                                    </option>
                                                ))}
                                            </select>
                                            <button
                                                type="button"
                                                className="fw-icon-btn fw-icon-btn--danger"
                                                onClick={() => removeAction(idx)}
                                                aria-label={`Remove step ${idx + 1}`}
                                                title="Remove step"
                                            >
                                                ×
                                            </button>
                                        </div>
                                    ))}
                                    <div className="acl-action-add-row">
                                        {ALL_KINDS.map(kind => (
                                            <button
                                                key={kind}
                                                type="button"
                                                className="fw-btn fw-btn--ghost acl-action-preset"
                                                onClick={() => addAction(kind)}
                                            >
                                                + {ACTION_KIND_LABELS[kind]}
                                            </button>
                                        ))}
                                    </div>
                                </div>
                                {hasNoTerminal && (
                                    <span className="fw-field__hint acl-hint--warn">
                                        No terminal action (pass/deny) — traffic will fall through.
                                    </span>
                                )}
                                {hasUnreachable && (
                                    <span className="fw-field__hint acl-hint--warn">
                                        Steps after step {terminalIdx + 1} are unreachable.
                                    </span>
                                )}
                            </div>
                            <div className="fw-field">
                                <label className="fw-field__label">
                                    Counter <span className="fw-field__optional">optional</span>
                                </label>
                                <input
                                    className="fw-input"
                                    placeholder="e.g. my_acl_counter"
                                    value={draft.counter}
                                    onChange={e => updateField('counter', e.target.value)}
                                />
                                <span className="fw-field__hint">Counter name shown in /stats. Leave empty to skip counting.</span>
                            </div>
                        </div>
                    </section>

                    <section className="fw-section">
                        <div className="fw-section-h">Match — addresses</div>
                        <div className="fw-section__body">
                            <div className="fw-fgrid">
                                <div className="fw-field">
                                    <label className="fw-field__label">
                                        Sources
                                        <span className="fw-field__count">{draft.sourceCidrs.length || 'any'}</span>
                                    </label>
                                    <ChipInput
                                        ref={sourceCidrsRef}
                                        value={draft.sourceCidrs}
                                        onChange={v => updateField('sourceCidrs', v)}
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
                                        onChange={v => updateField('dstCidrs', v)}
                                        placeholder="192.168.0.0/16…"
                                        kind="cidr"
                                        wildcardLabel="Any destination"
                                        validator={isValidCidr}
                                    />
                                </div>
                            </div>
                        </div>
                    </section>

                    <section className="fw-section">
                        <div className="fw-section-h">Match — ports and protocols</div>
                        <div className="fw-section__body">
                            <div className="fw-fgrid">
                                <div className="fw-field">
                                    <label className="fw-field__label">Src port ranges</label>
                                    <input
                                        className="fw-input fw-input--mono"
                                        placeholder="0-65535"
                                        value={draft.srcPortRaw}
                                        onChange={e => updateField('srcPortRaw', e.target.value)}
                                    />
                                    <span className="fw-field__hint">e.g. <code>80</code>, <code>80-90</code>, <code>80, 443</code>. Empty = any.</span>
                                </div>
                                <div className="fw-field">
                                    <label className="fw-field__label">Dst port ranges</label>
                                    <input
                                        className="fw-input fw-input--mono"
                                        placeholder="0-65535"
                                        value={draft.dstPortRaw}
                                        onChange={e => updateField('dstPortRaw', e.target.value)}
                                    />
                                    <span className="fw-field__hint">e.g. <code>80</code>, <code>443</code>, <code>8080-8443</code>. Empty = any.</span>
                                </div>
                            </div>
                            <div className="fw-field">
                                <label className="fw-field__label">Protocol ranges</label>
                                <input
                                    className="fw-input fw-input--mono"
                                    placeholder="1536-1791"
                                    value={draft.protoRaw}
                                    onChange={e => updateField('protoRaw', e.target.value)}
                                />
                                <span className="fw-field__hint">
                                    Encoded as <code>(ip_proto &lt;&lt; 8) | subtype</code>.
                                    TCP=<code>1536-1791</code>, UDP=<code>4352-4607</code>, ICMP=<code>256-511</code>.
                                    Empty = any.
                                </span>
                                <div className="acl-proto-presets">
                                    {[
                                        { label: '+ TCP', range: '1536-1791' },
                                        { label: '+ UDP', range: '4352-4607' },
                                        { label: '+ ICMP', range: '256-511' },
                                        { label: '+ ICMPv6', range: '14848-15103' },
                                        { label: '+ GRE', range: '12032-12287' },
                                        { label: '+ ESP', range: '12800-13055' },
                                    ].map(({ label, range }) => (
                                        <button
                                            key={range}
                                            type="button"
                                            className="fw-btn fw-btn--ghost acl-proto-preset"
                                            onClick={() => {
                                                const current = draft.protoRaw.trim();
                                                if (!current) {
                                                    updateField('protoRaw', range);
                                                } else if (!current.includes(range)) {
                                                    updateField('protoRaw', `${current}, ${range}`);
                                                }
                                            }}
                                        >
                                            {label}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        </div>
                    </section>

                    <section className="fw-section">
                        <div className="fw-section-h">Match — L2 / device</div>
                        <div className="fw-section__body">
                            <div className="fw-field">
                                <label className="fw-field__label">VLAN ranges</label>
                                <input
                                    className="fw-input fw-input--mono"
                                    placeholder="0-4095"
                                    value={draft.vlanRaw}
                                    onChange={e => updateField('vlanRaw', e.target.value)}
                                />
                                <span className="fw-field__hint">
                                    Single <code>100</code>, range <code>100-200</code>, list <code>100, 200, 300-400</code>. Empty = all VLANs.
                                </span>
                            </div>
                            <div className="fw-field">
                                <label className="fw-field__label">
                                    Devices
                                    <span className="fw-field__count">{draft.deviceNames.length || 'any'}</span>
                                </label>
                                <ChipInput
                                    ref={deviceNamesRef}
                                    value={draft.deviceNames}
                                    onChange={v => updateField('deviceNames', v)}
                                    placeholder="eth0, 0000:81:00.0…"
                                    kind="device"
                                    wildcardLabel="Any device"
                                    validator={isValidDeviceName}
                                />
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
