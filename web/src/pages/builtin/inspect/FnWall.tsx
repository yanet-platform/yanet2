import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { useFunctionCounters, useLaggedSeriesMap } from './hooks';
import { FunctionTile } from './FunctionTile';

export interface FnWallProps {
    instance: InstanceInfo;
}

/** 4-column grid of function tiles with traffic rates. */
export const FnWall: React.FC<FnWallProps> = ({ instance }) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];

    const deviceNames = useMemo(
        () => devices.map((d, idx) => d.name ?? `device-${idx}`),
        [devices],
    );
    const pipelineNames = useMemo(
        () => pipelines.map((p) => p.name ?? ''),
        [pipelines],
    );
    const functionNames = useMemo(
        () => functions.map((f) => f.name ?? ''),
        [functions],
    );

    const { rates } = useFunctionCounters(
        deviceNames,
        pipelineNames,
        functionNames,
        devices.length > 0 && pipelines.length > 0 && functions.length > 0,
    );

    const ppsMap = useMemo(() => {
        const m = new Map<string, number>();
        rates.forEach((r, name) => m.set(name, r.pps));
        return m;
    }, [rates]);

    const laggedSeries = useLaggedSeriesMap(ppsMap, 30, 1500);

    const activeCount = useMemo(() => {
        let count = 0;
        for (const f of functions) {
            const name = f.name ?? '';
            if ((rates.get(name)?.pps ?? 0) > 0) count += 1;
        }
        return count;
    }, [functions, rates]);

    const idleCount = functions.length - activeCount;

    return (
        <div className="iv-fn-wall">
            <div className="iv-fn-wall__header iv-label">
                <span>
                    FUNCTIONS{' '}
                    <span className="iv-label__count">{functions.length}</span>
                    <span className="iv-label__sub">
                        {' '}{activeCount} active · {idleCount} idle
                    </span>
                </span>
            </div>
            <div className="iv-fn-wall__grid iv-scroll">
                {functions.map((f, idx) => {
                    const name = f.name ?? `fn-${idx}`;
                    const rate = rates.get(name);
                    const pps = rate?.pps ?? 0;
                    const trend = laggedSeries.get(name) ?? [];

                    return (
                        <FunctionTile
                            key={name}
                            name={name}
                            pps={pps}
                            trend={trend}
                        />
                    );
                })}
            </div>
        </div>
    );
};
