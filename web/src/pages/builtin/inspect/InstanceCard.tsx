import React from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { HudHero } from './HudHero';
import { DeviceWall } from './DeviceWall';
import { ModuleStrip } from './ModuleStrip';
import { SystemAgents } from './SystemAgents';
import { PipeWall } from './PipeWall';
import { FnWall } from './FnWall';
import { useInstanceData } from '../_shared/useInstanceData';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

/** Root HUD layout for a single YANET instance. */
export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    const { rateCounters, absoluteCounters, physicalDeviceNames, usage: agentUsage, memTotals } = useInstanceData(instance);

    return (
        <div className="iv-instance">
            <HudHero
                instance={instance}
                rateCounters={rateCounters}
                physicalDeviceNames={physicalDeviceNames}
                memTotals={memTotals}
            />
            <DeviceWall
                instance={instance}
                rateCounters={rateCounters}
                absoluteCounters={absoluteCounters}
            />
            <ModuleStrip instance={instance} usage={agentUsage} />
            <SystemAgents instance={instance} usage={agentUsage} />
            <div className="iv-row">
                <PipeWall instance={instance} />
                <FnWall instance={instance} />
            </div>
        </div>
    );
};
