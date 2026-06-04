import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { usePipelineCounters } from './hooks';
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

    const { rates, series } = usePipelineCounters(
        deviceNames,
        pipelineNames,
        devices.length > 0 && pipelines.length > 0,
    );

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
                    const trend = series.get(name) ?? [];
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
