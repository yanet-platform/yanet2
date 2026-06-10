import React from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { AgentUsage } from './utils';
import { ModuleCardsGrid, type ModuleCardsChrome } from './ModuleCardsGrid';

export interface ModuleStripProps {
    instance: InstanceInfo;
    usage: Map<string, AgentUsage>;
}

const chrome: ModuleCardsChrome = {
    rootId: 'iv-section-modules',
    rootClass: 'iv-module-strip',
    headClass: 'iv-module-strip__header',
    labelClass: 'iv-label',
    countClass: 'iv-label__count',
    legendClass: 'iv-module-strip__legend',
    gridClass: 'iv-module-strip__grid',
    gridTemplateColumns: (n) => `repeat(${n || 1}, minmax(0, 1fr))`,
    cardClass: 'iv-module-card',
    dotClass: 'iv-dot',
    memUsedStyle: (used) => ({
        color: used > 0 ? 'var(--iv-text)' : 'var(--iv-mute)',
    }),
    memLimitClass: 'iv-module-card__mem-limit',
};

/** Horizontal strip showing all dataplane modules with usage indicators. */
export const ModuleStrip: React.FC<ModuleStripProps> = ({ instance, usage }) => (
    <ModuleCardsGrid instance={instance} usage={usage} chrome={chrome} />
);
