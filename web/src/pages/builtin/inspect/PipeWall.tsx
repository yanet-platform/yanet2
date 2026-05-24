import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { usePipelineCounters, useLaggedSeriesMap } from './hooks';
import { PipelineRow } from './PipelineRow';

export interface PipeWallProps {
    instance: InstanceInfo;
}

/** Vertical list of pipeline rows with traffic rates and sparklines. */
export const PipeWall: React.FC<PipeWallProps> = ({ instance }) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];

    const deviceNames = useMemo(
        () => devices.map((d, idx) => d.name ?? `device-${idx}`),
        [devices],
    );
    const pipelineNames = useMemo(
        () => pipelines.map((p) => p.name ?? ''),
        [pipelines],
    );

    const { rates } = usePipelineCounters(
        deviceNames,
        pipelineNames,
        devices.length > 0 && pipelines.length > 0,
    );

    const ppsMap = useMemo(() => {
        const m = new Map<string, number>();
        rates.forEach((r, name) => m.set(name, r.pps));
        return m;
    }, [rates]);

    const laggedSeries = useLaggedSeriesMap(ppsMap, 30, 1500);

    return (
        <div className="iv-pipe-wall">
            <div className="iv-label iv-pipe-wall__title">
                PIPELINES{' '}
                <span className="iv-label__count">{pipelines.length}</span>
            </div>
            <div className="iv-pipe-wall__list iv-scroll">
                {pipelines.map((p, idx) => {
                    const name = p.name ?? `pipeline-${idx}`;
                    const rate = rates.get(name);
                    const pps = rate?.pps ?? 0;
                    const trend = laggedSeries.get(name) ?? [];
                    const fns = p.functions ?? [];

                    return (
                        <PipelineRow
                            key={name}
                            name={name}
                            pps={pps}
                            fns={fns}
                            trend={trend}
                        />
                    );
                })}
            </div>
        </div>
    );
};
