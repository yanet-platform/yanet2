import React from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { AgentUsage } from '../inspect/utils';
import { ModuleCardsGrid, type ModuleCardsChrome } from '../inspect/ModuleCardsGrid';

export interface DataplaneModulesProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
}

const chrome: ModuleCardsChrome = {
    rootClass: 'dash-modules',
    headClass: 'dash-modules__head',
    gridClass: 'dash-modules__grid',
    gridTemplateColumns: (n) => `repeat(${Math.min(8, Math.max(1, n))}, minmax(0, 1fr))`,
    cardClass: 'dash-module-card',
    dotClass: 'dash-dot',
    countStyle: { color: 'var(--iv-text-dim)' },
    memUsedStyle: (used) => ({
        color: used > 0 ? 'var(--iv-text)' : 'var(--iv-mute)',
        fontVariantNumeric: 'tabular-nums',
    }),
    memLimitStyle: { color: 'var(--iv-mute)', fontSize: 9, fontVariantNumeric: 'tabular-nums' },
};

/** Full-width dataplane modules grid with routing and active-state indicators. */
export const DataplaneModules: React.FC<DataplaneModulesProps> = ({ instance, usage }) => (
    <ModuleCardsGrid instance={instance} usage={usage} chrome={chrome} />
);
