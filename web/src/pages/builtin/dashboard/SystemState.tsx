import React from 'react';

interface Pair {
    key: string;
    value: React.ReactNode;
    valueClassName?: string;
}

export interface SystemStateProps {
    workerCount?: number | null;
}

/** System-level status card. Fields without real API values render "—". */
export const SystemState: React.FC<SystemStateProps> = ({ workerCount }) => {
    const pairs: Pair[] = [
        { key: 'uptime',       value: '—' },
        { key: 'workers',      value: workerCount != null ? String(workerCount) : '—' },
        { key: 'controlplane', value: 'reachable', valueClassName: 'dash-system__ok' },
    ];

    return (
        <div className="dash-system">
            <div className="dash-system__label">SYSTEM STATE</div>
            <div className="dash-system__pairs">
                {pairs.map((p) => (
                    <div key={p.key} className="dash-system__pair">
                        <span className="dash-system__key">{p.key}</span>
                        <span className={p.valueClassName ?? 'dash-system__val'}>{p.value}</span>
                    </div>
                ))}
            </div>
        </div>
    );
};
