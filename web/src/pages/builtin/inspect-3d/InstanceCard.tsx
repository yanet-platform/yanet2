import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { useDeviceCounters } from '../../../hooks';
import { HudHero } from '../inspect/HudHero';
import { ModuleStrip } from '../inspect/ModuleStrip';
import { IsoScene3D } from './IsoScene3D';

export interface InstanceCardProps {
    instance: InstanceInfo;
}

/** Root layout for a single YANET instance: hero strip, 3D scene, and module strip. */
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

    return (
        <div className="iv3-instance">
            <HudHero
                instance={instance}
                rateCounters={rateCounters}
                physicalDeviceNames={physicalDeviceNames}
            />
            <IsoScene3D
                instance={instance}
                rateCounters={rateCounters}
                absoluteCounters={absoluteCounters}
            />
            <ModuleStrip instance={instance} />
        </div>
    );
};
