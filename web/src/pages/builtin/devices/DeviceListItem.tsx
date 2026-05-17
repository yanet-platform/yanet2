import React from 'react';
import { IconPlain, IconVlan } from './components/Icons';
import { MiniSpark } from './components/MiniSpark';
import { fmtPps } from './components/MiniSpark';
import type { LocalDevice } from './types';
import type { CounterHistoryEntry } from '../../../hooks/useCounterHistory';
import type { DeviceCounterData } from '../../../hooks/useDeviceCounters';

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
    const isVlan = device.type === 'vlan';
    const iconColor = isVlan ? 'var(--violet)' : 'var(--teal)';
    const name = device.id.name || '';
    const rxPps = counterData?.rx.pps ?? 0;
    const rxHistory = history?.rx ?? [];
    const txHistory = history?.tx ?? [];

    return (
        <button
            className={`dv-row${isSelected ? ' row-sel' : ''}`}
            onClick={onClick}
        >
            <span className="dv-row-icon" style={{ color: iconColor }}>
                {isVlan ? <IconVlan /> : <IconPlain />}
            </span>

            <span className="dv-row-main">
                <span className="dv-row-name">
                    <span className="dv-row-name-text">{name}</span>
                    {isVlan && device.vlanId !== undefined && (
                        <span className="dv-vid">{device.vlanId}</span>
                    )}
                </span>
                <span className="dv-row-sub">
                    {isVlan ? (
                        <>vlan · <span className="muted">—</span></>
                    ) : (
                        <>— · —</>
                    )}
                </span>
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
                <span className="dv-row-pps mono">{fmtPps(rxPps)}</span>
                <span className="dv-row-pps-lbl">pps</span>
            </span>

            <span className="dv-row-status">
                <span className="dv-link-dot" />
            </span>
        </button>
    );
};
