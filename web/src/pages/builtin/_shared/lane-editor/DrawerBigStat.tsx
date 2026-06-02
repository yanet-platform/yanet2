import React from 'react';

interface DrawerBigStatProps {
    prefix: 'fn' | 'pl';
    label: string;
    value: string;
    accent?: string;
}

/** Shared big-stat display for drawer counter sections. */
export const DrawerBigStat = ({ prefix, label, value, accent }: DrawerBigStatProps): React.JSX.Element => (
    <div className={`${prefix}-drawer__big-stat`}>
        <div className={`${prefix}-drawer__big-stat-label`}>{label}</div>
        <div
            className={`${prefix}-drawer__big-stat-value`}
            style={accent ? { color: accent } : undefined}
        >
            {value}
        </div>
    </div>
);
