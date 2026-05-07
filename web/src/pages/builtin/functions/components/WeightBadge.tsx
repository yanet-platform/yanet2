import React, { useState, useRef, useCallback } from 'react';

interface WeightBadgeProps {
    weight: number;
    onChange: (weight: number) => void;
}

/**
 * Clickable ×N badge that opens a small popover with a numeric input.
 */
export const WeightBadge: React.FC<WeightBadgeProps> = ({ weight, onChange }) => {
    const [open, setOpen] = useState(false);
    const [draft, setDraft] = useState(String(weight));
    const inputRef = useRef<HTMLInputElement>(null);

    const openPopover = useCallback((): void => {
        setDraft(String(weight));
        setOpen(true);
        setTimeout(() => { inputRef.current?.select(); }, 0);
    }, [weight]);

    const commit = useCallback((): void => {
        const n = parseInt(draft, 10);
        if (!isNaN(n) && n >= 0 && n <= 1000) {
            onChange(n);
        }
        setOpen(false);
    }, [draft, onChange]);

    const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLInputElement>): void => {
        if (e.key === 'Enter') {
            e.preventDefault();
            commit();
        } else if (e.key === 'Escape') {
            setOpen(false);
        }
    }, [commit]);

    return (
        <span className="fn-weight-badge-wrapper">
            <button
                className="fn-weight-badge"
                onClick={e => { e.stopPropagation(); openPopover(); }}
                title="Click to change weight"
                type="button"
            >
                ×{weight}
            </button>
            {open && (
                <div className="fn-weight-popover">
                    <label className="fn-weight-popover__label">Weight</label>
                    <input
                        ref={inputRef}
                        className="fn-weight-popover__input"
                        type="number"
                        min={0}
                        max={1000}
                        value={draft}
                        onChange={e => setDraft(e.target.value)}
                        onKeyDown={handleKeyDown}
                        onBlur={commit}
                        autoFocus
                    />
                </div>
            )}
        </span>
    );
};
