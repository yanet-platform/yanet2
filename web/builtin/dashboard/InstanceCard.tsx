import React from 'react';
import type { InstanceInfo } from '@yanet/core/api/inspect';
import { SystemState } from './SystemState';
import { useWorkerCount } from './hooks';
import { KpiStrip } from './KpiStrip';
import { IsoScene3D } from './IsoScene3D';
import { SceneErrorBoundary } from './SceneErrorBoundary';
import { Throughput } from './Throughput';
import { DataplaneModules } from './DataplaneModules';
import { SystemAgents } from '../inspect/SystemAgents';
import { useInstanceData } from '../_shared/useInstanceData';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

/** Root layout for a single YANET instance: system state, KPI strip, 3D scene, modules. */
export const InstanceCard: React.FC<InstanceCardProps> = ({ instance }) => {
    const { deviceNames, rateCounters, absoluteCounters, physicalDeviceNames, usage, memTotals } = useInstanceData(instance);

    const workerCount = useWorkerCount(deviceNames);

    return (
        <>
            <div className="dash-top-row">
                <div className="dash-top-row__left">
                    <SystemState workerCount={workerCount} memTotals={memTotals} />
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
                    usage={usage}
                />
            </SceneErrorBoundary>
            <DataplaneModules instance={instance} usage={usage} />
            <SystemAgents instance={instance} usage={usage} />
        </>
    );
};
