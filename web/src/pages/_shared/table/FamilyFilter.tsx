import React from 'react';

export type IPFamily = 'all' | 'v4' | 'v6';

const LABELS: Record<IPFamily, string> = {
    all: 'All',
    v4: 'IPv4',
    v6: 'IPv6',
};

export interface FamilyFilterProps {
    value: IPFamily;
    onChange: (family: IPFamily) => void;
}

/** Segmented All / IPv4 / IPv6 control for filtering rows by IP address family. */
export const FamilyFilter: React.FC<FamilyFilterProps> = ({ value, onChange }) => (
    <div className="yn-seg">
        {(['all', 'v4', 'v6'] as IPFamily[]).map((f) => (
            <button
                key={f}
                type="button"
                className={`yn-seg__btn${value === f ? ' yn-seg__btn--active' : ''}`}
                onClick={() => onChange(f)}
            >
                {LABELS[f]}
            </button>
        ))}
    </div>
);
