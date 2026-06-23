import { useMemo } from 'react';
import type { InstanceInfo } from '@yanet/core/api/inspect';
import { useDeviceCounters } from '@yanet/core/hooks';
import { deviceTypeManifest } from '@yanet/core/registry';
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

    // Names of devices that originate traffic into the dataplane, taken from
    // each device type's registry trafficSource flag.
    //
    // Stacked virtual devices (e.g. vlan) are excluded because their packets
    // also count on the parent physical device, which would double-count.
    const sourceDeviceNames = useMemo(() => {
        const result = new Set<string>();
        devices.forEach((d, idx) => {
            if (d.type && deviceTypeManifest(d.type)?.trafficSource) {
                result.add(d.name ?? `device-${idx}`);
            }
        });
        return result;
    }, [devices]);

    const usage = useMemo(() => computeAgentUsage(instance), [instance]);
    const memTotals = useMemo(() => computeMemoryTotals(usage), [usage]);

    return { devices, deviceNames, rateCounters, absoluteCounters, sourceDeviceNames, usage, memTotals };
};
