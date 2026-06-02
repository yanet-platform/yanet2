import React from 'react';
import { useChipInput, Chip } from '../../../components/chip-input';
import type { ChipInputProps, ChipInputHandle } from '../../../components/chip-input';

export type { ChipInputHandle };

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
    const { draft, inputRef, handleChange, handleKeyDown, handleBlur, handlePaste, startEdit, removeChip } =
        useChipInput(value, onChange, ref);

    const isWildcard = value.length === 0;

    return (
        <div
            className="yn-chip-input"
            onClick={() => inputRef.current?.focus()}
        >
            {isWildcard && wildcardLabel && (
                <span className="yn-badge-any">{wildcardLabel}</span>
            )}
            {value.map((v, idx) => (
                <Chip
                    key={idx}
                    value={v}
                    index={idx}
                    valid={validator(v)}
                    onEdit={startEdit}
                    onRemove={removeChip}
                />
            ))}
            <input
                ref={inputRef}
                type="text"
                value={draft}
                placeholder={value.length ? '' : placeholder}
                onChange={handleChange}
                onKeyDown={handleKeyDown}
                onBlur={handleBlur}
                onPaste={handlePaste}
                className="yn-chip-input__raw"
            />
        </div>
    );
});

ChipInput.displayName = 'ChipInput';

export default ChipInput;
