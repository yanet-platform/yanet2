import React, { useMemo } from 'react';
import type { DeviceCounterData } from '../../../hooks';
import { useRollingSeries } from '../inspect/hooks';
import { Sparkline } from '../inspect/Sparkline';
import { fmtBps, fmtPps } from '../inspect/formatters';

export interface ThroughputProps {
    rateCounters: Map<string, DeviceCounterData>;
    physicalDeviceNames: Set<string>;
}

/** Aggregate throughput hero with a full-width sparkline beneath. */
export const Throughput: React.FC<ThroughputProps> = ({ rateCounters, physicalDeviceNames }) => {
    const { aggregatePps, aggregateBps } = useMemo(() => {
        let pps = 0;
        let bps = 0;
        rateCounters.forEach((d, name) => {
            if (!physicalDeviceNames.has(name)) return;
            pps += d.rx?.pps ?? 0;
            bps += d.rx?.bps ?? 0;
        });
        return { aggregatePps: pps, aggregateBps: bps };
    }, [rateCounters, physicalDeviceNames]);

    const throughputSeries = useRollingSeries(aggregatePps, 60);

    return (
        <div className="dash-throughput">
            <div className="dash-throughput__top">
                <span className="dash-throughput__label">THROUGHPUT</span>
                <span className="dash-throughput__row">
                    <span className="dash-throughput__big">{fmtBps(aggregateBps)}</span>
                    <span className="dash-throughput__unit">bps</span>
                    <span className="dash-throughput__sep">·</span>
                    <span className="dash-throughput__pps">
                        {fmtPps(aggregatePps)}{' '}
                        <span className="dash-throughput__pps-unit">pps</span>
                    </span>
                </span>
            </div>
            <Sparkline
                data={throughputSeries}
                color="var(--iv-accent)"
                fill
                w={720}
                h={36}
            />
        </div>
    );
};
