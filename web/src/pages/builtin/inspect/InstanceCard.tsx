import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { useDeviceCounters } from '../../../hooks';
import { HudHero } from './HudHero';
import { DeviceWall } from './DeviceWall';
import { ModuleStrip } from './ModuleStrip';
import { SystemAgents } from './SystemAgents';
import { PipeWall } from './PipeWall';
import { FnWall } from './FnWall';
import { computeAgentUsage, computeMemoryTotals } from './utils';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

/** Root HUD layout for a single YANET instance. */
export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    const devices = instance.devices ?? [];

    const deviceNames = useMemo(
        () => devices.map((d, idx) => d.name ?? `device-${idx}`),
        [devices],
    );

    const { counters: rateCounters, absoluteCounters } = useDeviceCounters(
        deviceNames,
        devices.length > 0,
    );

    const physicalDeviceNames = useMemo(() => {
        const result = new Set<string>();
        devices.forEach((d, idx) => {
            if (d.type === 'plain') {
                result.add(d.name ?? `device-${idx}`);
            }
        });
        return result;
    }, [devices]);

    const agentUsage = useMemo(() => computeAgentUsage(instance), [instance]);
    const memTotals = useMemo(() => computeMemoryTotals(agentUsage), [agentUsage]);

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
