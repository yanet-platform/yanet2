import React, { useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import type { InstanceInfo } from '../../../api/inspect';
import { MODULE_DESCRIPTIONS, MODULE_ROUTES, computeModulePipelineUsage } from '../inspect/utils';

export interface DataplaneModulesProps {
    instance: InstanceInfo;
}

/** Full-width dataplane modules grid with routing and active-state indicators. */
export const DataplaneModules: React.FC<DataplaneModulesProps> = ({ instance }) => {
    const navigate = useNavigate();
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

    const pipeUsage = useMemo(() => computeModulePipelineUsage(instance), [instance]);

    const moduleData = useMemo(
        () =>
            modules.map((m) => {
                const name = m.name ?? '';
                const cfg = configs.filter(
                    (c) => (c.type?.toLowerCase() ?? '') === name.toLowerCase(),
                ).length;
                const pipe = pipeUsage.get(name.toLowerCase()) ?? 0;
                const inUse = cfg > 0 || pipe > 0;
                return { name, cfg, pipe, inUse, desc: MODULE_DESCRIPTIONS[name] ?? '' };
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
                    const href = MODULE_ROUTES[m.name];
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
                            key={m.name}
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
                        </div>
                    );
                })}
            </div>
        </div>
    );
};
