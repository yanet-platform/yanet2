import React from 'react';

interface ChipProps {
    value: string;
    index: number;
    valid: boolean;
    onEdit: (index: number) => void;
    onRemove: (index: number) => void;
}

/** Single chip token with click-to-edit and remove affordances. */
export const Chip: React.FC<ChipProps> = ({ value, index, valid, onEdit, onRemove }) => (
    <span className={`yn-chip${valid ? '' : ' yn-chip--invalid'}`}>
        <span
            className="yn-chip__label"
            role="button"
            tabIndex={0}
            title="Click to edit"
            onClick={(e) => { e.stopPropagation(); onEdit(index); }}
            onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    onEdit(index);
                }
            }}
        >
            {value}
        </span>
        <button
            type="button"
            className="yn-chip__x"
            onClick={(e) => { e.stopPropagation(); onRemove(index); }}
            aria-label={`Remove ${value}`}
        >
            ×
        </button>
    </span>
);
