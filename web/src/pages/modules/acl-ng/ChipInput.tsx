import React, { useEffect, useImperativeHandle, useRef, useState, useMemo, useCallback } from 'react';
import { Dialog, TextInput } from '@gravity-ui/uikit';

type ChipKind = 'cidr' | 'device' | 'vlan';

interface ChipInputProps {
    value: string[];
    onChange: (values: string[]) => void;
    placeholder?: string;
    kind: ChipKind;
    wildcardLabel?: string;
    validator: (s: string) => boolean;
}

/** Imperative handle for synchronously flushing pending text before a parent save. */
export interface ChipInputHandle {
    /**
     * Synchronously return any tokens in the current draft text and clear it.
     * Does NOT call onChange — the caller merges the returned tokens itself so
     * that draft text and committed chips land in one synchronous transaction.
     */
    flush(): string[];
}

const MANY_THRESHOLD = 20;

interface BulkPasteModalProps {
    existing: string[];
    validate: (s: string) => boolean;
    onClose: () => void;
    onApply: (items: string[], mode: 'append' | 'replace') => void;
}

const BulkPasteModal: React.FC<BulkPasteModalProps> = ({ existing, validate, onClose, onApply }) => {
    const [text, setText] = useState('');
    const [mode, setMode] = useState<'append' | 'replace'>('append');

    const parsed = useMemo(
        () => text.split(/[\s,;]+/).map(s => s.trim()).filter(Boolean),
        [text],
    );

    const invalid = useMemo(
        () => parsed.filter(v => !validate(v)),
        [parsed, validate],
    );

    const deduped = useMemo(() => {
        const seen = new Set(existing);
        const result: string[] = [];
        for (const item of parsed) {
            if (!seen.has(item)) {
                seen.add(item);
                result.push(item);
            }
        }
        return result;
    }, [parsed, existing]);

    const dupCount = parsed.length - deduped.length;
    const validCount = mode === 'replace'
        ? parsed.filter(v => validate(v)).length
        : deduped.filter(v => validate(v)).length;

    const handleApply = useCallback((): void => {
        const validItems = mode === 'replace'
            ? parsed.filter(v => validate(v))
            : deduped.filter(v => validate(v));
        onApply(validItems, mode);
    }, [parsed, deduped, mode, validate, onApply]);

    return (
        <Dialog open onClose={onClose} size="m">
            <Dialog.Header caption="Bulk paste" />
            <Dialog.Body>
                <p style={{ margin: '0 0 10px', fontSize: 12.5, color: 'var(--fw-text-3)' }}>
                    Whitespace, comma, or newline separated values.
                </p>
                <textarea
                    autoFocus
                    className="fw-input fw-input--mono"
                    style={{ width: '100%', height: 220, resize: 'vertical', fontFamily: 'var(--fw-font-mono)', fontSize: 12 }}
                    value={text}
                    placeholder={'10.0.0.0/24\n10.0.1.0/24, 10.0.2.0/24\n2001:db8::/32'}
                    onChange={e => setText(e.target.value)}
                    spellCheck={false}
                />
                <div style={{ display: 'flex', gap: 14, marginTop: 10, fontSize: 12.5, flexWrap: 'wrap', alignItems: 'center' }}>
                    <span style={{ color: 'var(--fw-text-3)' }}>parsed:</span>
                    <span style={{ fontFamily: 'var(--fw-font-mono)' }}>{parsed.length} total</span>
                    {invalid.length > 0 && (
                        <span style={{ color: 'var(--g-color-text-danger)', fontFamily: 'var(--fw-font-mono)' }}>
                            {invalid.length} invalid
                        </span>
                    )}
                    {dupCount > 0 && (
                        <span style={{ color: 'var(--fw-text-3)', fontFamily: 'var(--fw-font-mono)' }}>
                            {dupCount} duplicate
                        </span>
                    )}
                    <span style={{ fontFamily: 'var(--fw-font-mono)', color: 'var(--g-color-text-warning)' }}>
                        {validCount} will be {mode === 'replace' ? 'set' : 'added'}
                    </span>
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                    <button
                        type="button"
                        className={`fw-btn ${mode === 'append' ? 'fw-btn--primary' : 'fw-btn--ghost'}`}
                        onClick={() => setMode('append')}
                    >
                        Append
                    </button>
                    <button
                        type="button"
                        className={`fw-btn ${mode === 'replace' ? 'fw-btn--primary' : 'fw-btn--ghost'}`}
                        onClick={() => setMode('replace')}
                    >
                        Replace all
                    </button>
                </div>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleApply}
                textButtonCancel="Cancel"
                textButtonApply={mode === 'replace' ? `Replace with ${validCount}` : `Append ${validCount}`}
            />
        </Dialog>
    );
};

interface ListPopoverProps {
    items: string[];
    label: string;
    onClose: () => void;
}

const ListPopover: React.FC<ListPopoverProps> = ({ items, label, onClose }) => {
    const [q, setQ] = useState('');
    const filtered = q.trim()
        ? items.filter(s => s.toLowerCase().includes(q.trim().toLowerCase()))
        : items;

    let stats = `${items.length} ${label}`;
    const hasCidrs = items.some(s => s.includes('/'));
    if (hasCidrs) {
        let v4 = 0, v6 = 0;
        for (const s of items) (s.includes(':') ? v6++ : v4++);
        stats = `${items.length} total · ${v4} v4 · ${v6} v6`;
    }

    const copyAll = (): void => {
        navigator.clipboard.writeText(items.join('\n')).catch(() => undefined);
    };

    return (
        <Dialog open onClose={onClose} size="m">
            <Dialog.Header caption={label} />
            <Dialog.Body>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10, minHeight: 0, maxHeight: '50vh' }}>
                    <div style={{ fontSize: 12, color: 'var(--fw-text-3)', marginBottom: 4 }}>{stats}</div>
                    <TextInput
                        autoFocus
                        value={q}
                        onUpdate={setQ}
                        placeholder={`Filter ${items.length} ${label}…`}
                        hasClear
                        size="s"
                    />
                    <div style={{
                        flex: 1,
                        overflow: 'auto',
                        background: 'var(--fw-bg-2)',
                        border: '1px solid var(--fw-line)',
                        borderRadius: 6,
                        padding: 8,
                        display: 'flex',
                        flexWrap: 'wrap',
                        gap: 5,
                        alignContent: 'flex-start',
                        minHeight: 80,
                        maxHeight: 320,
                    }}>
                        {filtered.length === 0 ? (
                            <span style={{ color: 'var(--fw-text-3)', fontSize: 12, padding: 12 }}>No matches.</span>
                        ) : (
                            filtered.map((s, idx) => {
                                const isV6 = s.includes(':');
                                const cls = s.includes('/')
                                    ? (isV6 ? 'acl-chip acl-chip--ipv6' : 'acl-chip acl-chip--ipv4')
                                    : 'acl-chip acl-chip--device';
                                return (
                                    <span key={idx} className={cls} title={s}>{s}</span>
                                );
                            })
                        )}
                    </div>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 12 }}>
                        <span style={{ color: 'var(--fw-text-3)' }}>
                            Showing {filtered.length} of {items.length}
                        </span>
                        <button type="button" className="fw-btn fw-btn--ghost" style={{ fontSize: 12, padding: '2px 10px' }} onClick={copyAll}>
                            Copy all
                        </button>
                    </div>
                </div>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                textButtonCancel="Close"
            />
        </Dialog>
    );
};

/**
 * Token chip input with bulk-paste dialog and scrollable chip list for large sets.
 * Accepts comma-separated paste, Enter/Tab to commit, Backspace to remove last.
 * Exposes a flush() handle so parent forms can collect pending text before saving
 * without relying on the asynchronous onBlur → setState path.
 *
 * When the chip count exceeds 20, the container becomes scrollable and shows a
 * filter input to search within visible chips.
 */
const ChipInput = React.forwardRef<ChipInputHandle, ChipInputProps>(({
    value,
    onChange,
    placeholder,
    wildcardLabel,
    validator,
}, ref) => {
    const [draft, setDraft] = useState('');
    const draftRef = useRef('');
    const inputRef = useRef<HTMLInputElement>(null);
    const [filter, setFilter] = useState('');
    const [bulkOpen, setBulkOpen] = useState(false);
    const [popoverOpen, setPopoverOpen] = useState(false);

    useEffect(() => {
        draftRef.current = draft;
    }, [draft]);

    useImperativeHandle(ref, () => ({
        flush() {
            const tokens = draftRef.current
                .split(/[,\s]+/)
                .map((t) => t.trim())
                .filter(Boolean);
            if (tokens.length > 0) {
                setDraft('');
                draftRef.current = '';
            }
            return tokens;
        },
    }), []);

    const commitDraft = (raw?: string): void => {
        const source = raw ?? draft;
        const tokens = source.split(/[,\s]+/).map((t) => t.trim()).filter(Boolean);
        if (!tokens.length) return;
        onChange([...value, ...tokens]);
        setDraft('');
        draftRef.current = '';
    };

    const handleChange = (e: React.ChangeEvent<HTMLInputElement>): void => {
        const v = e.target.value;
        if (v.includes(',')) {
            commitDraft(v);
        } else {
            setDraft(v);
            draftRef.current = v;
        }
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
        if ((e.key === 'Enter' || e.key === 'Tab') && draft.trim()) {
            e.preventDefault();
            commitDraft();
        } else if (e.key === 'Backspace' && !draft && value.length > 0) {
            onChange(value.slice(0, -1));
        }
    };

    const handleBlur = (): void => {
        if (draft.trim()) commitDraft();
    };

    const handlePaste = (e: React.ClipboardEvent<HTMLInputElement>): void => {
        const text = e.clipboardData.getData('text');
        if (/[,\s]/.test(text)) {
            e.preventDefault();
            commitDraft(text);
        }
    };

    const isMany = value.length > MANY_THRESHOLD;
    const isWildcard = value.length === 0;

    const filteredIndices = useMemo(() => {
        if (!filter.trim()) return value.map((_, idx) => idx);
        const q = filter.trim().toLowerCase();
        return value.map((v, idx) => ({ v, idx })).filter(({ v }) => v.toLowerCase().includes(q)).map(({ idx }) => idx);
    }, [value, filter]);

    const kindLabel = (): string => {
        switch (true) {
            case value.some(v => v.includes('/')): return 'CIDRs';
            case value.some(v => v.includes(':')): return 'addresses';
            default: return 'items';
        }
    };

    // Summary for table-cell collapse (>6 items).
    const isBig = value.length > 6;

    return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {isMany && (
                <div className="acl-chip-toolbar">
                    <div style={{ flex: 1 }}>
                        <TextInput
                            size="s"
                            value={filter}
                            onUpdate={setFilter}
                            placeholder={`Filter ${value.length} items…`}
                            hasClear
                        />
                    </div>
                    <button
                        type="button"
                        className="fw-btn fw-btn--ghost"
                        style={{ fontSize: 11, padding: '2px 8px' }}
                        onClick={() => setBulkOpen(true)}
                    >
                        Paste bulk
                    </button>
                    {isBig && (
                        <button
                            type="button"
                            className="fw-btn fw-btn--ghost"
                            style={{ fontSize: 11, padding: '2px 8px' }}
                            onClick={() => setPopoverOpen(true)}
                            title="View full list"
                        >
                            View all
                        </button>
                    )}
                    <button
                        type="button"
                        className="fw-btn fw-btn--ghost"
                        style={{ fontSize: 11, padding: '2px 8px', color: 'var(--g-color-text-danger)' }}
                        onClick={() => onChange([])}
                        title="Remove all"
                    >
                        Clear
                    </button>
                    <span style={{ fontSize: 11, color: 'var(--fw-text-3)', fontFamily: 'var(--fw-font-mono)' }}>
                        {value.length}
                    </span>
                </div>
            )}

            <div
                className={`fw-chip-input${isMany ? ' fw-chip-input--many' : ''}`}
                onClick={() => !filter && inputRef.current?.focus()}
            >
                {isWildcard && wildcardLabel && (
                    <span className="acl-chip acl-chip--any">{wildcardLabel}</span>
                )}
                {filteredIndices.map(idx => {
                    const v = value[idx];
                    const valid = validator(v);
                    return (
                        <span key={idx} className={`fw-chip${valid ? '' : ' fw-chip--invalid'}`}>
                            <span
                                className="fw-chip__label"
                                role="button"
                                tabIndex={0}
                                title="Click to edit"
                                onClick={(e) => {
                                    e.stopPropagation();
                                    onChange(value.filter((_, j) => j !== idx));
                                    setDraft(v);
                                    draftRef.current = v;
                                    setTimeout(() => inputRef.current?.focus(), 0);
                                }}
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter' || e.key === ' ') {
                                        e.preventDefault();
                                        onChange(value.filter((_, j) => j !== idx));
                                        setDraft(v);
                                        draftRef.current = v;
                                        setTimeout(() => inputRef.current?.focus(), 0);
                                    }
                                }}
                            >
                                {v}
                            </span>
                            <button
                                type="button"
                                className="fw-chip__x"
                                onClick={(e) => {
                                    e.stopPropagation();
                                    onChange(value.filter((_, j) => j !== idx));
                                }}
                                aria-label={`Remove ${v}`}
                            >
                                ×
                            </button>
                        </span>
                    );
                })}
                {filter && filteredIndices.length === 0 && (
                    <span style={{ fontSize: 12, color: 'var(--fw-text-3)', fontFamily: 'var(--fw-font-mono)' }}>
                        no matches
                    </span>
                )}
                {!filter && (
                    <input
                        ref={inputRef}
                        type="text"
                        value={draft}
                        placeholder={value.length ? '' : placeholder}
                        onChange={handleChange}
                        onKeyDown={handleKeyDown}
                        onBlur={handleBlur}
                        onPaste={handlePaste}
                        className="fw-chip-input__raw"
                    />
                )}
            </div>

            {!isMany && (
                <button
                    type="button"
                    className="fw-btn fw-btn--ghost"
                    style={{ fontSize: 11, alignSelf: 'flex-start', padding: '2px 8px' }}
                    onClick={() => setBulkOpen(true)}
                >
                    + Paste many
                </button>
            )}

            {bulkOpen && (
                <BulkPasteModal
                    existing={value}
                    validate={validator}
                    onClose={() => setBulkOpen(false)}
                    onApply={(items, pasteMode) => {
                        if (pasteMode === 'replace') {
                            onChange(items);
                        } else {
                            const seen = new Set(value);
                            onChange([...value, ...items.filter(i => !seen.has(i))]);
                        }
                        setBulkOpen(false);
                    }}
                />
            )}

            {popoverOpen && (
                <ListPopover
                    items={value}
                    label={kindLabel()}
                    onClose={() => setPopoverOpen(false)}
                />
            )}
        </div>
    );
});

ChipInput.displayName = 'ChipInput';

export default ChipInput;
