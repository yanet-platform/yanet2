import { useMemo } from 'react';
import type { InstanceInfo } from '@yanet/core/api/inspect';
import { useDeviceCounters } from '@yanet/core/hooks';
import { computeAgentUsage, computeMemoryTotals } from '../inspect/utils';

/** Shared data preamble for dashboard and inspect InstanceCard components. */
export const useInstanceData = (instance: InstanceInfo) => {
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

    const usage = useMemo(() => computeAgentUsage(instance), [instance]);
    const memTotals = useMemo(() => computeMemoryTotals(usage), [usage]);

    return { devices, deviceNames, rateCounters, absoluteCounters, physicalDeviceNames, usage, memTotals };
};
