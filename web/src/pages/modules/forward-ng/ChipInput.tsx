import React, { useEffect, useImperativeHandle, useRef, useState } from 'react';

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

/**
 * Token chip input.
 * Accepts comma-separated paste, Enter/Tab to commit, Backspace to remove last.
 * Exposes a flush() handle so parent forms can collect pending text before saving
 * without relying on the asynchronous onBlur → setState path.
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

    const isWildcard = value.length === 0;

    return (
        <div
            className="fwng-chip-input"
            onClick={() => inputRef.current?.focus()}
        >
            {isWildcard && wildcardLabel && (
                <span className="fwng-badge-any">{wildcardLabel}</span>
            )}
            {value.map((v, idx) => {
                const valid = validator(v);
                return (
                    <span key={idx} className={`fwng-chip${valid ? '' : ' fwng-chip--invalid'}`}>
                        <span
                            className="fwng-chip__label"
                            role="button"
                            tabIndex={0}
                            title="Click to edit"
                            onClick={(e) => {
                                e.stopPropagation();
                                // Remove the chip and place its text in the draft input for editing.
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
                            className="fwng-chip__x"
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
            <input
                ref={inputRef}
                type="text"
                value={draft}
                placeholder={value.length ? '' : placeholder}
                onChange={handleChange}
                onKeyDown={handleKeyDown}
                onBlur={handleBlur}
                onPaste={handlePaste}
                className="fwng-chip-input__raw"
            />
        </div>
    );
});

ChipInput.displayName = 'ChipInput';

export default ChipInput;
