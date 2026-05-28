import React, { useMemo } from 'react';
import type { DeviceInfo } from '../../../api/inspect';
import type { DeviceCounterData, DeviceAbsoluteData } from '../../../hooks';
import { Sparkline } from './Sparkline';
import { IconPort, IconTag } from './icons';
import { fmtBps, fmtPps } from './formatters';

export interface DeviceTileProps {
    device: DeviceInfo;
    name: string;
    rateRow: DeviceCounterData | undefined;
    absRow: DeviceAbsoluteData | undefined;
    trend: number[];
}

/** Single device card in the DeviceWall grid. */
export const DeviceTile: React.FC<DeviceTileProps> = ({ device, name, rateRow, absRow, trend }) => {
    const kind = device.type === 'plain' ? 'plain' : 'vlan';

    const status = useMemo((): 'ok' | 'idle' => {
        if (!absRow) return 'idle';
        return absRow.rx.packets > 0 || absRow.tx.packets > 0 ? 'ok' : 'idle';
    }, [absRow]);

    const accentColor = kind === 'vlan' ? 'var(--iv-link)' : 'var(--iv-accent)';
    const borderColor = status === 'ok' ? accentColor : 'var(--iv-dim)';

    return (
        <div
            className="iv-device-tile"
            style={{ borderLeftColor: borderColor }}
        >
            <div className="iv-device-tile__header">
                <span style={{ color: accentColor, display: 'flex', alignItems: 'center' }}>
                    {kind === 'vlan' ? <IconTag size={11} /> : <IconPort size={11} />}
                </span>
                <span className="iv-device-tile__name">{name}</span>
                <span
                    className="iv-dot"
                    style={{ background: status === 'ok' ? 'var(--iv-ok)' : 'var(--iv-idle)' }}
                />
            </div>
            <div className="iv-device-tile__rates">
                <span className="iv-device-tile__pps">{fmtPps(rateRow?.rx?.pps ?? 0)}</span>
                <span className="iv-device-tile__bps">{fmtBps(rateRow?.rx?.bps ?? 0)}bps</span>
            </div>
            <Sparkline
                data={trend}
                w={132}
                h={16}
                color={kind === 'vlan' ? 'var(--iv-link)' : 'var(--iv-accent)'}
                fill
            />
        </div>
    );
};
