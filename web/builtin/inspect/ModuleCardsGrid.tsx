import React from 'react';
import { useNavigate } from 'react-router-dom';
import type { InstanceInfo } from '@yanet/core/api/inspect';
import { fmtIEC } from './formatters';
import { MemoryBar } from './MemoryBar';
import { getModuleCardAgentUsage, getModuleRoute } from './utils';
import type { AgentUsage } from './utils';
import { useModuleCards } from './useModuleCards';

/** Visual knobs that differ between the inspect and dashboard renderings. */
export interface ModuleCardsChrome {
    rootId?: string;
    rootClass: string;
    headClass: string;
    labelClass?: string;
    countClass?: string;
    countStyle?: React.CSSProperties;
    legendClass?: string;
    gridClass: string;
    gridTemplateColumns: (moduleCount: number) => string;
    cardClass: string;
    dotClass: string;
    memUsedStyle: (used: number) => React.CSSProperties;
    memLimitClass?: string;
    memLimitStyle?: React.CSSProperties;
}

export interface ModuleCardsGridProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
    chrome: ModuleCardsChrome;
}

/** Shared module-card grid renderer used by ModuleStrip and DataplaneModules. */
export const ModuleCardsGrid: React.FC<ModuleCardsGridProps> = ({ instance, usage, chrome }) => {
    const navigate = useNavigate();
    const modules = instance.dp_modules ?? [];
    const moduleData = useModuleCards(instance);

    return (
        <div id={chrome.rootId} className={chrome.rootClass}>
            <div className={chrome.headClass}>
                <span className={chrome.labelClass}>
                    DATAPLANE MODULES{' '}
                    <span className={chrome.countClass} style={chrome.countStyle}>
                        {modules.length}
                    </span>
                </span>
                <span className={chrome.legendClass}>
                    <span style={{ color: 'var(--iv-ok)' }}>●</span>
                    {' in use   '}
                    <span style={{ color: 'var(--iv-idle)' }}>○</span>
                    {' available'}
                </span>
            </div>
            <div
                className={chrome.gridClass}
                style={{ gridTemplateColumns: chrome.gridTemplateColumns(modules.length) }}
            >
                {moduleData.map((m) => {
                    const href = getModuleRoute(m.name);
                    const isClickable = Boolean(href);
                    const cardClassName = [
                        chrome.cardClass,
                        m.inUse && chrome.cardClass + '--active',
                        isClickable && chrome.cardClass + '--clickable',
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
                            className={cardClassName}
                            onClick={handleClick}
                            onKeyDown={handleKeyDown}
                            tabIndex={isClickable ? 0 : undefined}
                            role={isClickable ? 'link' : undefined}
                        >
                            <div className={chrome.cardClass + '__top'}>
                                <span className={chrome.cardClass + '__name'}>{m.name}</span>
                                <span
                                    className={chrome.dotClass}
                                    style={{ background: m.inUse ? 'var(--iv-ok)' : 'var(--iv-idle)' }}
                                />
                            </div>
                            <div className={chrome.cardClass + '__desc'}>{m.desc}</div>
                            <div className={chrome.cardClass + '__stats'}>{m.cfg}cfg · {m.pipe}pipe</div>
                            {(() => {
                                const mem = getModuleCardAgentUsage(usage, m.name);
                                if (!mem) return null;
                                return (
                                    <div className={chrome.cardClass + '__mem'}>
                                        <div className={chrome.cardClass + '__mem-row'}>
                                            <span style={chrome.memUsedStyle(mem.used)}>
                                                {fmtIEC(mem.used)}
                                            </span>
                                            <span
                                                className={chrome.memLimitClass}
                                                style={chrome.memLimitStyle}
                                            >
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
