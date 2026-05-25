import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import type { DeviceCounterData } from '../../../hooks';
import { useRollingSeries } from './hooks';
import { HeroSparkline } from './HeroSparkline';
import { Crosshair } from './Crosshair';
import { RadialPulse } from './RadialPulse';
import { SideKpi } from './SideKpi';
import { fmtBps, fmtIEC, fmtPps } from './formatters';
import { MemoryBar } from './MemoryBar';
import type { MemoryTotals } from './utils';

export interface HudHeroProps {
    instance: InstanceInfo;
    rateCounters: Map<string, DeviceCounterData>;
    physicalDeviceNames: Set<string>;
    memTotals: MemoryTotals;
}

interface SideKpiMemoryProps {
    totals: MemoryTotals;
}

/** Memory KPI cell — aggregate used/limit bar with hottest agent callout. */
const SideKpiMemory: React.FC<SideKpiMemoryProps> = ({ totals }) => (
    <div className="iv-side-kpi">
        <div className="iv-side-kpi__label">MEMORY</div>
        <div
            style={{
                fontSize: 22,
                fontWeight: 500,
                letterSpacing: -0.5,
                lineHeight: 1.05,
                display: 'flex',
                alignItems: 'baseline',
                gap: 6,
            }}
        >
            <span>{fmtIEC(totals.memUsed)}</span>
            <span style={{ fontSize: 11, color: 'var(--iv-text-dim)' }}>
                / {fmtIEC(totals.memLimit)}
            </span>
        </div>
        <MemoryBar used={totals.memUsed} limit={totals.memLimit} height={4} cells={28} />
        <div style={{ color: 'var(--iv-text-dim)', fontSize: 10 }}>
            {totals.agentsActive}/{totals.agents} agents
            {totals.hot !== null && (
                <span style={{ color: 'var(--iv-mute)' }}>
                    {' · hot: '}
                    <span style={{ color: 'var(--iv-text-dim)' }}>{totals.hot.name}</span>
                </span>
            )}
        </div>
    </div>
);

/** Top hero panel showing aggregate throughput and key KPI side columns. */
export const HudHero: React.FC<HudHeroProps> = ({
    instance,
    rateCounters,
    physicalDeviceNames,
    memTotals,
}) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];
    const modules = instance.dp_modules ?? [];
    const configs = instance.cp_configs ?? [];

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

    const throughputSeries = useRollingSeries(aggregatePps, 90);

    return (
        <div className="iv-hero">
            <div className="iv-ambient-scan" aria-hidden />
            <Crosshair pos="tl" />
            <Crosshair pos="tr" />
            <Crosshair pos="bl" />
            <Crosshair pos="br" />

            <div className="iv-hero__side iv-hero__side--left">
                <SideKpi label="DEVICES" primary={devices.length} />
                <SideKpi label="PIPELINES" primary={pipelines.length} />
                <SideKpi label="FUNCTIONS" primary={functions.length} />
            </div>

            <div className="iv-hero__center">
                <RadialPulse />
                <div className="iv-hero__caption">AGGREGATE THROUGHPUT · RX</div>
                <div className="iv-hero__throughput">
                    <span className="iv-hero__throughput-value">{fmtBps(aggregateBps)}</span>
                    <span className="iv-hero__throughput-unit">bps</span>
                </div>
                <div className="iv-hero__meta">
                    <span className="iv-hero__meta-pps">
                        {fmtPps(aggregatePps)}{' '}
                        <span className="iv-hero__meta-dim">pps</span>
                    </span>
                    <span className="iv-hero__meta-dim">— workers</span>
                </div>
                <div className="iv-hero__sparkline">
                    <HeroSparkline data={throughputSeries} />
                </div>
            </div>

            <div className="iv-hero__side iv-hero__side--right">
                <SideKpi label="MODULES" primary={modules.length} />
                <SideKpi label="CONFIGS" primary={configs.length} />
                <SideKpiMemory totals={memTotals} />
            </div>
        </div>
    );
};
