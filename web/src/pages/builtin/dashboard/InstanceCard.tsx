import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { useDeviceCounters } from '../../../hooks';
import { SystemState } from './SystemState';
import { useWorkerCount } from './hooks';
import { KpiStrip } from './KpiStrip';
import { IsoScene3D } from './IsoScene3D';
import { SceneErrorBoundary } from './SceneErrorBoundary';
import { Throughput } from './Throughput';
import { DataplaneModules } from './DataplaneModules';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

/** Root layout for a single YANET instance: system state, KPI strip, 3D scene, modules. */
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

    const workerCount = useWorkerCount(deviceNames);

    const physicalDeviceNames = useMemo(() => {
        const result = new Set<string>();
        devices.forEach((d, idx) => {
            if (d.type === 'plain') {
                result.add(d.name ?? `device-${idx}`);
            }
        });
        return result;
    }, [devices]);

    return (
        <>
            <div className="dash-top-row">
                <div className="dash-top-row__left">
                    <SystemState workerCount={workerCount} />
                    <KpiStrip instance={instance} />
                </div>
                <div className="dash-top-row__right">
                    <Throughput rateCounters={rateCounters} physicalDeviceNames={physicalDeviceNames} />
                </div>
            </div>
            <SceneErrorBoundary>
                <IsoScene3D
                    instance={instance}
                    rateCounters={rateCounters}
                    absoluteCounters={absoluteCounters}
                />
            </SceneErrorBoundary>
            <DataplaneModules instance={instance} />
        </>
    );
};
