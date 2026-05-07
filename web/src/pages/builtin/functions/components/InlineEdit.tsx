import React, { useState, useRef, useCallback } from 'react';

/** Pencil icon shown on hover for editable text. */
const PencilIcon = (): React.JSX.Element => (
    <svg
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.8"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        style={{ flexShrink: 0 }}
    >
        <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
        <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
    </svg>
);

export interface InlineEditProps {
    value: string;
    onChange: (value: string) => void;
    validate?: (value: string) => string | null;
    placeholder?: string;
    className?: string;
    /** 'chain' adds dashed underline + pencil icon on hover; 'module' adds dashed underline only. */
    hintVariant?: 'chain' | 'module';
}

/**
 * Inline text editor: displays as text, switches to input on click.
 * Enter or blur commits; Esc reverts. Validation errors show red underline.
 */
export const InlineEdit: React.FC<InlineEditProps> = ({
    value,
    onChange,
    validate,
    placeholder,
    className,
    hintVariant,
}) => {
    const [editing, setEditing] = useState(false);
    const [draft, setDraft] = useState(value);
    const [error, setError] = useState<string | null>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    const startEdit = useCallback((e?: React.SyntheticEvent): void => {
        e?.stopPropagation();
        setDraft(value);
        setError(null);
        setEditing(true);
        setTimeout(() => {
            inputRef.current?.select();
        }, 0);
    }, [value]);

    const tryCommit = useCallback((): void => {
        const trimmed = draft.trim();
        const err = validate ? validate(trimmed) : null;
        if (err) {
            setError(err);
            return;
        }
        setEditing(false);
        setError(null);
        if (trimmed !== value) {
            onChange(trimmed);
        }
    }, [draft, validate, value, onChange]);

    const revert = useCallback((): void => {
        setEditing(false);
        setError(null);
        setDraft(value);
    }, [value]);

    const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>): void => {
        if (e.key === 'Enter') {
            e.preventDefault();
            tryCommit();
        } else if (e.key === 'Escape') {
            e.preventDefault();
            revert();
        }
    }, [tryCommit, revert]);

    const handleBlur = useCallback((): void => {
        if (editing) {
            tryCommit();
        }
    }, [editing, tryCommit]);

    if (editing) {
        return (
            <span style={{ position: 'relative', display: 'inline-block' }}>
                <input
                    ref={inputRef}
                    className={`inline-edit-input${error ? ' inline-edit-input--error' : ''}${className ? ` ${className}` : ''}`}
                    value={draft}
                    onChange={e => { setDraft(e.target.value); setError(null); }}
                    onKeyDown={handleKeyDown}
                    onBlur={handleBlur}
                    placeholder={placeholder}
                    autoFocus
                />
                {error && (
                    <span className="inline-edit-error-tooltip">{error}</span>
                )}
            </span>
        );
    }

    if (hintVariant) {
        const hintClass = hintVariant === 'chain' ? 'inline-edit-text inline-edit-text--hint-chain' : 'inline-edit-text inline-edit-text--hint-module';
        return (
            <span
                className={`${hintClass}${className ? ` ${className}` : ''}`}
                onClick={startEdit}
                role="button"
                tabIndex={0}
                onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { startEdit(e); } }}
                title="Click to edit"
            >
                {value || placeholder || '—'}
                {hintVariant === 'chain' && (
                    <span className="inline-edit-pencil-icon">
                        <PencilIcon />
                    </span>
                )}
            </span>
        );
    }

    return (
        <span
            className={`inline-edit-text${className ? ` ${className}` : ''}`}
            onClick={startEdit}
            role="button"
            tabIndex={0}
            onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { startEdit(e); } }}
            title="Click to edit"
        >
            {value || placeholder || '—'}
        </span>
    );
};
