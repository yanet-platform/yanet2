import React, { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import type { InstanceInfo } from '../../../api/inspect';
import {
    computeModulePipelineUsage,
    getModuleCardAgentUsage,
    getModuleDescription,
    getModuleRoute,
    normalizeModuleName,
    type AgentUsage,
} from '../inspect/utils';
import { MemoryBar } from '../inspect/MemoryBar';
import { fmtIEC } from '../inspect/formatters';

export interface DataplaneModulesProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
}

/** Full-width dataplane modules grid with routing and active-state indicators. */
export const DataplaneModules: React.FC<DataplaneModulesProps> = ({ instance, usage }) => {
    const navigate = useNavigate();
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    const pipeUsage = useMemo(() => computeModulePipelineUsage(instance), [instance]);

    const moduleData = useMemo(
        () =>
            modules.map((m, idx) => {
                const name = m.name ?? '';
                const key = name || `module-${idx}`;
                const moduleKey = normalizeModuleName(name);
                const cfg = configs.filter(
                    (c) => normalizeModuleName(c.type ?? '') === moduleKey,
                ).length;
                const pipe = pipeUsage.get(moduleKey) ?? 0;
                const inUse = cfg > 0 || pipe > 0;
                return { key, name, cfg, pipe, inUse, desc: getModuleDescription(name) };
            }),
        [modules, configs, pipeUsage],
    );

    const colCount = Math.min(9, Math.max(1, modules.length));

    return (
        <div className="dash-modules">
            <div className="dash-modules__head">
                <span>
                    DATAPLANE MODULES{' '}
                    <span style={{ color: 'var(--iv-text-dim)' }}>{modules.length}</span>
                </span>
                <span>
                    <span style={{ color: 'var(--iv-ok)' }}>●</span>
                    {' in use   '}
                    <span style={{ color: 'var(--iv-idle)' }}>○</span>
                    {' available'}
                </span>
            </div>
            <div
                className="dash-modules__grid"
                style={{ gridTemplateColumns: `repeat(${colCount}, minmax(0, 1fr))` }}
            >
                {moduleData.map((m) => {
                    const href = getModuleRoute(m.name);
                    const isClickable = Boolean(href);
                    const className = [
                        'dash-module-card',
                        m.inUse && 'dash-module-card--active',
                        isClickable && 'dash-module-card--clickable',
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
                            key={m.key}
                            className={className}
                            onClick={handleClick}
                            onKeyDown={handleKeyDown}
                            tabIndex={isClickable ? 0 : undefined}
                            role={isClickable ? 'link' : undefined}
                        >
                            <div className="dash-module-card__top">
                                <span className="dash-module-card__name">{m.name}</span>
                                <span
                                    className="dash-dot"
                                    style={{
                                        background: m.inUse ? 'var(--iv-ok)' : 'var(--iv-idle)',
                                    }}
                                />
                            </div>
                            <div className="dash-module-card__desc">{m.desc}</div>
                            <div className="dash-module-card__stats">{m.cfg}cfg · {m.pipe}pipe</div>
                            {(() => {
                                const ag = getModuleCardAgentUsage(usage, m.name);
                                if (!ag) return null;
                                return (
                                    <div className="dash-module-card__mem">
                                        <div className="dash-module-card__mem-row">
                                            <span
                                                style={{
                                                    color: ag.used > 0
                                                        ? 'var(--iv-text)'
                                                        : 'var(--iv-mute)',
                                                    fontVariantNumeric: 'tabular-nums',
                                                }}
                                            >
                                                {fmtIEC(ag.used)}
                                            </span>
                                            <span
                                                style={{
                                                    color: 'var(--iv-mute)',
                                                    fontSize: 9,
                                                    fontVariantNumeric: 'tabular-nums',
                                                }}
                                            >
                                                {fmtIEC(ag.limit)}
                                            </span>
                                        </div>
                                        <MemoryBar used={ag.used} limit={ag.limit} height={4} cells={20} />
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
