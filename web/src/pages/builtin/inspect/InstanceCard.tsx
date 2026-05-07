import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { useDeviceCounters } from '../../../hooks';
import { KpiBar } from './KpiBar';
import {
    ModulesSection,
    ConfigurationsSection,
    FunctionsSection,
    PipelinesSection,
    DevicesSection,
} from './sections';
import { useThroughputSeries } from './hooks';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

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

    const { current: throughputPps, series: throughputSeries } = useThroughputSeries(rateCounters);

    return (
        <div className="inspect-instance">
            <KpiBar
                instance={instance}
                deviceCounters={rateCounters}
                deviceAbsolute={absoluteCounters}
                throughputPps={throughputPps}
                throughputSeries={throughputSeries}
            />
            <DevicesSection
                instance={instance}
                rateCounters={rateCounters}
                absoluteCounters={absoluteCounters}
            />
            <div className="inspect-row-2">
                <PipelinesSection instance={instance} />
                <FunctionsSection instance={instance} />
            </div>
            <ModulesSection instance={instance} />
            <ConfigurationsSection instance={instance} />
        </div>
    );
};
