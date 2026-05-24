import React from 'react';
import { useLaggedValue } from './hooks';
import { Sparkline } from './Sparkline';
import { IconFn } from './icons';
import { fmtPps } from './formatters';

export interface FunctionTileProps {
    name: string;
    pps: number;
    trend: number[];
}

/** Single function tile in FnWall with lag-interpolated PPS display. */
export const FunctionTile: React.FC<FunctionTileProps> = ({ name, pps, trend }) => {
    const smoothPps = useLaggedValue(pps, 1500);
    const active = pps > 0;

    return (
        <div className={`iv-fn-tile${active ? ' iv-fn-tile--active' : ''}`}>
            <div className="iv-fn-tile__header">
                <span style={{ color: active ? 'var(--iv-accent)' : 'var(--iv-mute)', display: 'flex', alignItems: 'center' }}>
                    <IconFn size={10} />
                </span>
                <span className="iv-fn-tile__name">{name}</span>
            </div>
            <div className="iv-fn-tile__bottom">
                <span className="iv-fn-tile__pps">
                    {fmtPps(smoothPps)}
                    <span className="iv-pps-unit"> pps</span>
                </span>
                <Sparkline data={trend} w={48} h={12} color="var(--iv-accent)" />
            </div>
        </div>
    );
};
