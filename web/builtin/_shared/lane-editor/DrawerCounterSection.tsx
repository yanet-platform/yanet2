import React from 'react';
import type { InterpolatedCounterData } from '@yanet/core/hooks';
import { formatPps, formatBps } from '@yanet/core/utils';
import { DrawerBigStat } from './DrawerBigStat';
import { Sparkline } from './Sparkline';

interface DrawerCounterSectionProps {
    prefix: string;
    counter: InterpolatedCounterData | undefined;
    accent: string;
    sparklineData: number[];
}

/** Live-counters section rendered inside a lane drawer. */
export const DrawerCounterSection = ({ prefix, counter, accent, sparklineData }: DrawerCounterSectionProps): React.JSX.Element => (
    <div className={`${prefix}-drawer__section`}>
        <div className={`${prefix}-drawer__section-label`}>Live counters</div>
        <div className={`${prefix}-drawer__counters-grid`}>
            <DrawerBigStat prefix={prefix} label="PPS" value={counter ? formatPps(counter.pps) : '—'} accent={accent} />
            <DrawerBigStat prefix={prefix} label="BPS" value={counter ? formatBps(counter.bps) : '—'} />
        </div>
        {sparklineData.length >= 4 && (
            <div>
                <div className={`${prefix}-drawer__sparkline-label`}>pps · last {sparklineData.length} samples</div>
                <div className={`${prefix}-drawer__sparkline`}>
                    <Sparkline data={sparklineData} width={364} height={48} color={accent} />
                </div>
            </div>
        )}
    </div>
);
