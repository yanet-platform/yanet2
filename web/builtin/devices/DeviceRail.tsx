import React, { useState, useCallback } from 'react';
import { MiniSpark } from './components/MiniSpark';
import { IconCaret } from './components/Icons';
import { formatPps } from '@yanet/core/utils';
import { deviceTypeManifest } from '@yanet/core/registry';
import type { LocalDevice } from './types';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';

interface PopAnchor {
    left: number;
    top: number;
}

interface DeviceRailItemProps {
    device: LocalDevice;
    isSelected: boolean;
    counterData: DeviceCounterData | undefined;
    history: CounterHistoryEntry | undefined;
    onClick: () => void;
}

const DeviceRailItem = ({
    device,
    isSelected,
    counterData,
    history,
    onClick,
}: DeviceRailItemProps): React.JSX.Element => {
    const manifest = deviceTypeManifest(device.type);
    const Icon = manifest?.icon;
    const iconColor = manifest?.accentColor ?? 'var(--teal)';
    const name = device.id.name || '';
    const typeLabel = manifest?.typeDescription ?? device.type;

    // Fixed-position anchor for the hover popover so it escapes the rail's
    // own scroll clipping; null while not hovered.
    const [anchor, setAnchor] = useState<PopAnchor | null>(null);

    const handleEnter = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
        const rect = e.currentTarget.getBoundingClientRect();
        setAnchor({ left: rect.right + 12, top: rect.top + rect.height / 2 });
    }, []);

    const handleLeave = useCallback(() => setAnchor(null), []);

    return (
        <div
            className={`dv-rail-item${isSelected ? ' rail-sel' : ''}`}
            onMouseEnter={handleEnter}
            onMouseLeave={handleLeave}
        >
            <button
                id={`dv-row-${name}`}
                className="dv-rail-btn"
                onClick={onClick}
                style={{ color: iconColor }}
            >
                <span className="dv-rail-icon">{Icon && <Icon />}</span>
                <span className="dv-rail-short">{name}</span>
            </button>

            {anchor && (
                <div className="dv-rail-pop" style={{ left: anchor.left, top: anchor.top }}>
                    <div className="dv-rail-pop-hd">
                        <span className="dv-rail-pop-dot" style={{ background: iconColor }} />
                        <span className="dv-rail-pop-name">{name}</span>
                    </div>
                    <div className="dv-rail-pop-type">{typeLabel}</div>
                    <MiniSpark
                        deviceId={`rail-${name}`}
                        rx={history?.rx ?? []}
                        tx={history?.tx ?? []}
                        width={198}
                        height={34}
                    />
                    <div className="dv-rail-pop-metric">
                        <span><span className="lbl">RX </span>{formatPps(counterData?.rx.pps ?? 0)}</span>
                        <span><span className="lbl">TX </span>{formatPps(counterData?.tx.pps ?? 0)}</span>
                    </div>
                </div>
            )}
        </div>
    );
};

export interface DeviceRailProps {
    devices: LocalDevice[];
    selectedDeviceName: string | null;
    onSelectDevice: (deviceName: string) => void;
    onExpand: () => void;
    counters: Map<string, DeviceCounterData>;
    history: Map<string, CounterHistoryEntry>;
}

/** Collapsed list variant: a narrow icon rail with hover detail popovers. */
export const DeviceRail: React.FC<DeviceRailProps> = ({
    devices,
    selectedDeviceName,
    onSelectDevice,
    onExpand,
    counters,
    history,
}) => (
    <div className="dv-rail">
        <button className="dv-icon-btn" onClick={onExpand} title="Expand list">
            <IconCaret dir="right" />
        </button>
        <div className="dv-rail-divider" />
        <div className="dv-rail-scroll">
            {devices.map(d => (
                <DeviceRailItem
                    key={d.id.name}
                    device={d}
                    isSelected={d.id.name === selectedDeviceName}
                    counterData={counters.get(d.id.name || '')}
                    history={history.get(d.id.name || '')}
                    onClick={() => onSelectDevice(d.id.name || '')}
                />
            ))}
        </div>
    </div>
);
