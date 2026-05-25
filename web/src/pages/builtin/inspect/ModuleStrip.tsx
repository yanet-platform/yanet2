import React, { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import type { InstanceInfo } from '../../../api/inspect';
import { fmtIEC } from './formatters';
import { MemoryBar } from './MemoryBar';
import { MODULE_DESCRIPTIONS, MODULE_ROUTES, computeModulePipelineUsage } from './utils';
import type { AgentUsage } from './utils';

export interface ModuleStripProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
}

/** Horizontal strip showing all dataplane modules with usage indicators. */
export const ModuleStrip: React.FC<ModuleStripProps> = ({ instance, usage }) => {
    const navigate = useNavigate();
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    const pipeUsage = useMemo(() => computeModulePipelineUsage(instance), [instance]);

    const moduleData = useMemo(
        () =>
            modules.map((m) => {
                const name = m.name ?? '';
                const cfg = configs.filter((c) => (c.type?.toLowerCase() ?? '') === name.toLowerCase()).length;
                const pipe = pipeUsage.get(name.toLowerCase()) ?? 0;
                const inUse = cfg > 0 || pipe > 0;
                return { name, cfg, pipe, inUse, desc: MODULE_DESCRIPTIONS[name] ?? '' };
            }),
        [modules, configs, pipeUsage],
    );

    return (
        <div className="iv-module-strip">
            <div className="iv-module-strip__header">
                <span className="iv-label">
                    DATAPLANE MODULES{' '}
                    <span className="iv-label__count">{modules.length}</span>
                </span>
                <span className="iv-module-strip__legend">
                    <span style={{ color: 'var(--iv-ok)' }}>●</span>
                    {' in use   '}
                    <span style={{ color: 'var(--iv-idle)' }}>○</span>
                    {' available'}
                </span>
            </div>
            <div
                className="iv-module-strip__grid"
                style={{ gridTemplateColumns: `repeat(${modules.length || 1}, minmax(0, 1fr))` }}
            >
                {moduleData.map((m) => {
                    const href = MODULE_ROUTES[m.name];
                    const isClickable = Boolean(href);
                    const className = [
                        'iv-module-card',
                        m.inUse && 'iv-module-card--active',
                        isClickable && 'iv-module-card--clickable',
                    ].filter(Boolean).join(' ');
                    const handleClick = href ? () => navigate(href) : undefined;
                    const handleKeyDown = href
                        ? (e: React.KeyboardEvent<HTMLDivElement>) => {
                              if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault();
                                  navigate(href);
                              }
                          }
                        : undefined;
                    return (
                        <div
                            key={m.name}
                            className={className}
                            onClick={handleClick}
                            onKeyDown={handleKeyDown}
                            tabIndex={isClickable ? 0 : undefined}
                            role={isClickable ? 'link' : undefined}
                        >
                            <div className="iv-module-card__top">
                                <span className="iv-module-card__name">{m.name}</span>
                                <span
                                    className="iv-dot"
                                    style={{ background: m.inUse ? 'var(--iv-ok)' : 'var(--iv-idle)' }}
                                />
                            </div>
                            <div className="iv-module-card__desc">{m.desc}</div>
                            <div className="iv-module-card__stats">{m.cfg}cfg · {m.pipe}pipe</div>
                            {(() => {
                                const mem = usage.get(m.name);
                                if (!mem) return null;
                                return (
                                    <div className="iv-module-card__mem">
                                        <div className="iv-module-card__mem-row">
                                            <span
                                                style={{
                                                    color:
                                                        mem.used > 0
                                                            ? 'var(--iv-text)'
                                                            : 'var(--iv-mute)',
                                                }}
                                            >
                                                {fmtIEC(mem.used)}
                                            </span>
                                            <span className="iv-module-card__mem-limit">
                                                {fmtIEC(mem.limit)}
                                            </span>
                                        </div>
                                        <MemoryBar
                                            used={mem.used}
                                            limit={mem.limit}
                                            height={4}
                                            cells={20}
                                        />
                                    </div>
                                );
                            })()}
                        </div>
                    );
                })}
            </div>
        </div>
    );
};
