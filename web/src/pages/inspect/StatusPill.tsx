import React from 'react';

export type PillState = 'ok' | 'idle' | 'warn' | 'err';

export interface StatusPillProps {
    label: string;
    state?: PillState;
    dot?: boolean;
}

export const StatusPill: React.FC<StatusPillProps> = ({ label, state, dot = true }) => {
    return (
        <span className="inspect-pill" data-state={state}>
            {state && dot && <span className="inspect-pill-dot" />}
            {label}
        </span>
    );
};
