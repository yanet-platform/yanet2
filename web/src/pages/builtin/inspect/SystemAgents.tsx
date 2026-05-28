import React from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { AgentUsage } from './utils';
import { fmtIEC } from './formatters';
import { MemoryBar } from './MemoryBar';
import './SystemAgents.scss';

export interface SystemAgentsProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
}

const META_ORDER = ['function', 'pipeline'];

/** Compact horizontal strip for system and meta agents (plain/vlan/function/pipeline). */
export const SystemAgents: React.FC<SystemAgentsProps> = ({ instance: _instance, usage }) => {
    const systems: AgentUsage[] = [];
    const metas: AgentUsage[] = [];

    for (const u of usage.values()) {
        if (u.kind === 'system') systems.push(u);
        else if (u.kind === 'meta') metas.push(u);
    }

    if (systems.length === 0 && metas.length === 0) return null;

    systems.sort((a, b) => a.name.localeCompare(b.name));
    metas.sort((a, b) => {
        const ai = META_ORDER.indexOf(a.name);
        const bi = META_ORDER.indexOf(b.name);
        const an = ai === -1 ? META_ORDER.length : ai;
        const bn = bi === -1 ? META_ORDER.length : bi;
        return an - bn || a.name.localeCompare(b.name);
    });

    const agents = [...systems, ...metas];
    const count = agents.length;

    return (
        <div className="iv-system-agents">
            <div className="iv-system-agents__header iv-system-agents__title">
                SYSTEM AGENTS
            </div>
            <div
                className="iv-system-agents__grid"
                style={{ gridTemplateColumns: `repeat(${count}, 1fr)` }}
            >
                {agents.map((a) => {
                    const accent = a.kind === 'meta' ? 'var(--iv-link)' : 'var(--iv-accent)';
                    const active = a.used > 0;
                    return (
                        <div key={a.name} className="iv-system-agents__row">
                            <div className="iv-system-agents__name">
                                <span
                                    className="iv-system-agents__dot"
                                    style={{
                                        background: active ? accent : 'var(--iv-idle)',
                                    }}
                                />
                                <span className="iv-system-agents__name-text">{a.name}</span>
                            </div>
                            <MemoryBar
                                used={a.used}
                                limit={a.limit}
                                height={4}
                                cells={20}
                                color={accent}
                            />
                            <span className="iv-system-agents__value">
                                <span
                                    style={{
                                        color: a.used > 0 ? 'var(--iv-text)' : 'var(--iv-mute)',
                                    }}
                                >
                                    {fmtIEC(a.used)}
                                </span>
                                {' '}
                                <span style={{ color: 'var(--iv-mute)' }}>
                                    / {fmtIEC(a.limit)}
                                </span>
                            </span>
                        </div>
                    );
                })}
            </div>
        </div>
    );
};
