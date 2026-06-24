import React from 'react';
import { MiniSpark } from './components/MiniSpark';
import { formatPps } from '@yanet/core/utils';
import { deviceTypeManifest } from '@yanet/core/registry';
import type { LocalDevice } from './types';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';

export interface DeviceListItemProps {
    device: LocalDevice;
    isSelected: boolean;
    counterData: DeviceCounterData | undefined;
    history: CounterHistoryEntry | undefined;
    onClick: () => void;
}

export const DeviceListItem: React.FC<DeviceListItemProps> = ({
    device,
    isSelected,
    counterData,
    history,
    onClick,
}) => {
    const manifest = deviceTypeManifest(device.type);
    const Icon = manifest?.icon;
    const iconColor = manifest?.accentColor ?? 'var(--teal)';
    const badge = manifest?.rowBadge?.(device);
    const subtitle = manifest?.rowSubtitle?.(device) ?? '— · —';
    const name = device.id.name || '';
    const rxPps = counterData?.rx.pps ?? 0;
    const txPps = counterData?.tx.pps ?? 0;
    const rxHistory = history?.rx ?? [];
    const txHistory = history?.tx ?? [];

    return (
        <button
            id={`dv-row-${device.id.name || ''}`}
            className={`dv-row${isSelected ? ' row-sel' : ''}`}
            onClick={onClick}
        >
            <span className="dv-row-icon" style={{ color: iconColor }}>
                {Icon && <Icon />}
            </span>

            <span className="dv-row-main">
                <span className="dv-row-name">
                    <span className="dv-row-name-text">{name}</span>
                    {badge !== undefined && (
                        <span className="dv-vid">{badge}</span>
                    )}
                </span>
                <span className="dv-row-sub">{subtitle}</span>
            </span>

            <span className="dv-row-spark">
                <MiniSpark
                    deviceId={name}
                    rx={rxHistory}
                    tx={txHistory}
                    width={72}
                    height={24}
                />
            </span>

            <span className="dv-row-metric">
                <span className="dv-row-pps-entry">
                    <span className="dv-row-pps mono">{formatPps(rxPps)}</span>
                    <span className="dv-row-pps-lbl dv-lbl-rx">RX</span>
                </span>
                <span className="dv-row-pps-entry">
                    <span className="dv-row-pps mono">{formatPps(txPps)}</span>
                    <span className="dv-row-pps-lbl dv-lbl-tx">TX</span>
                </span>
            </span>

            <span className="dv-row-status">
                <span className="dv-link-dot" />
            </span>
        </button>
    );
};
