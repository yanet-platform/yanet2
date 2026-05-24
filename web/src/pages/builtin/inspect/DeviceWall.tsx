import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { DeviceCounterData, DeviceAbsoluteData } from '../../../hooks';
import { useDeviceTrendSeries } from './hooks';
import { DeviceTile } from './DeviceTile';

export interface DeviceWallProps {
    instance: InstanceInfo;
    rateCounters: Map<string, DeviceCounterData>;
    absoluteCounters: Map<string, DeviceAbsoluteData>;
}

/** Grid of device tiles showing per-device RX traffic sparklines. */
export const DeviceWall: React.FC<DeviceWallProps> = ({
    instance,
    rateCounters,
    absoluteCounters,
}) => {
    const devices = instance.devices ?? [];

    const rxTrend = useDeviceTrendSeries(rateCounters, 'rx');

    const phyCount = useMemo(
        () => devices.filter((d) => d.type === 'plain').length,
        [devices],
    );
    const vlanCount = useMemo(
        () => devices.filter((d) => d.type !== 'plain').length,
        [devices],
    );

    return (
        <div className="iv-device-wall">
            <div className="iv-device-wall__header">
                <span className="iv-label">
                    DEVICES{' '}
                    <span className="iv-label__count">{devices.length}</span>
                    <span className="iv-label__plain"> ◼ plain ·</span>
                    <span className="iv-label__vlan"> ◆ vlan</span>
                </span>
                <span className="iv-device-wall__legend">
                    {phyCount} phy · {vlanCount} vlan
                </span>
            </div>
            <div className="iv-device-wall__grid iv-scroll">
                {devices.map((device, idx) => {
                    const name = device.name ?? `device-${idx}`;
                    return (
                        <DeviceTile
                            key={name}
                            device={device}
                            name={name}
                            rateRow={rateCounters.get(name)}
                            absRow={absoluteCounters.get(name)}
                            trend={rxTrend.get(name) ?? []}
                        />
                    );
                })}
            </div>
        </div>
    );
};
