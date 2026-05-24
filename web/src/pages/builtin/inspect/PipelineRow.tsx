import React from 'react';
import { useLaggedValue } from './hooks';
import { Sparkline } from './Sparkline';
import { fmtPps } from './formatters';

export interface PipelineRowProps {
    name: string;
    pps: number;
    fns: string[];
    trend: number[];
}

/** Single pipeline row in PipeWall with lag-interpolated PPS display. */
export const PipelineRow: React.FC<PipelineRowProps> = ({ name, pps, fns, trend }) => {
    const smoothPps = useLaggedValue(pps, 1500);
    const active = pps > 0;

    return (
        <div className={`iv-pipe-row${active ? ' iv-pipe-row--active' : ''}`}>
            <span className="iv-pipe-row__name">
                <span
                    className="iv-dot"
                    style={{ background: active ? 'var(--iv-ok)' : 'var(--iv-idle)' }}
                />
                {name}
            </span>
            <span className="iv-pipe-row__fns">
                {fns.length > 0 ? fns.join(', ') : 'no functions'}
            </span>
            <Sparkline data={trend} w={70} h={20} color="var(--iv-accent)" />
            <span className="iv-pipe-row__pps">
                {fmtPps(smoothPps)}
                <span className="iv-pps-unit"> pps</span>
            </span>
        </div>
    );
};
