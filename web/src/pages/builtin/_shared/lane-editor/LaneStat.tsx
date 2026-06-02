import React from 'react';

interface LaneStatProps {
    prefix: 'fn' | 'pl';
    label: string;
    value: string | number;
    accent?: boolean;
}

export const LaneStat = ({ prefix, label, value, accent }: LaneStatProps): React.JSX.Element => (
    <div className={`${prefix}-card-header__stat`}>
        <span
            className={`${prefix}-card-header__stat-value`}
            style={accent ? { color: `var(--${prefix}-accent)` } : undefined}
        >
            {value}
        </span>
        <span className={`${prefix}-card-header__stat-label`}>{label}</span>
    </div>
);
