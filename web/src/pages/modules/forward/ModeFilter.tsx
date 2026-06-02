import React from 'react';

export type ModeFilterValue = 'all' | 'in' | 'out' | 'none';

const LABELS: Record<ModeFilterValue, string> = {
    all: 'All',
    in: 'IN',
    out: 'OUT',
    none: 'NONE',
};

export interface ModeFilterProps {
    value: ModeFilterValue;
    onChange: (v: ModeFilterValue) => void;
}

/** Segmented All / IN / OUT / NONE control for filtering forward rules by mode. */
export const ModeFilter: React.FC<ModeFilterProps> = ({ value, onChange }) => (
    <div className="yn-seg">
        {(['all', 'in', 'out', 'none'] as ModeFilterValue[]).map((m) => (
            <button
                key={m}
                type="button"
                className={`yn-seg__btn${value === m ? ' yn-seg__btn--active' : ''}`}
                onClick={() => onChange(m)}
            >
                {LABELS[m]}
            </button>
        ))}
    </div>
);
