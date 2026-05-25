import React from 'react';
import type { MemoryTotals } from '../inspect/utils';
import { fmtIEC } from '../inspect/formatters';
import { MemoryBar } from '../inspect/MemoryBar';

interface Pair {
    key: string;
    value: React.ReactNode;
    valueClassName?: string;
}

export interface SystemStateProps {
    workerCount?: number | null;
    memTotals?: MemoryTotals;
}

/** System-level status card. Fields without real API values render "—". */
export const SystemState: React.FC<SystemStateProps> = ({ workerCount, memTotals }) => {
    const memValue: React.ReactNode = memTotals && memTotals.memLimit > 0
        ? (
            <div className="dash-system__mem">
                <MemoryBar used={memTotals.memUsed} limit={memTotals.memLimit} height={4} cells={20} />
                <div className="dash-system__mem-pop">
                    <div>
                        <span style={{ color: 'var(--iv-text)' }}>{fmtIEC(memTotals.memUsed)}</span>
                        <span style={{ color: 'var(--iv-mute)' }}>{' / '}{fmtIEC(memTotals.memLimit)}</span>
                    </div>
                    <div style={{ color: 'var(--iv-text-dim)' }}>
                        {(memTotals.memUsed / memTotals.memLimit * 100).toFixed(2)}% used
                    </div>
                    {memTotals.hot != null && (
                        <div>
                            <span style={{ color: 'var(--iv-mute)' }}>{'hottest: '}</span>
                            <span style={{ color: 'var(--iv-text-dim)' }}>{memTotals.hot.name}</span>
                        </div>
                    )}
                    {memTotals.agents > 0 && (
                        <div style={{ color: 'var(--iv-mute)' }}>
                            {memTotals.agentsActive}/{memTotals.agents} active
                        </div>
                    )}
                </div>
            </div>
        )
        : '—';

    const pairs: Pair[] = [
        { key: 'controlplane', value: 'reachable', valueClassName: 'dash-system__ok' },
        { key: 'workers',      value: workerCount != null ? String(workerCount) : '—' },
        { key: 'memory',       value: memValue, valueClassName: 'dash-system__mem-cell' },
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
