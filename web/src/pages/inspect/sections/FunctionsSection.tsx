import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../api/inspect';
import { InspectCard } from '../InspectCard';
import { FunctionTile } from '../FunctionTile';
import { EmptyState } from '../../../components';
import { useFunctionCounters } from '../hooks';

export interface FunctionsSectionProps {
    instance: InstanceInfo;
}

export const FunctionsSection: React.FC<FunctionsSectionProps> = ({ instance }) => {
    const functions = instance.functions ?? [];
    const pipelines = instance.pipelines ?? [];
    const devices = instance.devices ?? [];

    const deviceNames = useMemo(
        () => devices.map((d, idx) => d.name ?? `device-${idx}`),
        [devices],
    );
    const pipelineNames = useMemo(
        () => pipelines.map((p, idx) => p.name ?? `pipeline-${idx}`),
        [pipelines],
    );
    const functionNames = useMemo(
        () => functions.map((f, idx) => f.name ?? `function-${idx}`),
        [functions],
    );

    const { rates, series } = useFunctionCounters(
        deviceNames,
        pipelineNames,
        functionNames,
        functions.length > 0,
    );

    return (
        <InspectCard title="Functions" count={functions.length}>
            {functions.length > 0 ? (
                <div className="inspect-fn-grid">
                    {functions.map((f, idx) => {
                        const name = f.name ?? `function-${idx}`;
                        const chains = (f.chains ?? []).map((c) => c.name ?? 'unnamed');
                        const rate = rates.get(name);
                        const ser = series.get(name) ?? [];
                        return (
                            <FunctionTile
                                key={name}
                                name={name}
                                chains={chains}
                                pps={rate?.pps ?? 0}
                                series={ser}
                            />
                        );
                    })}
                </div>
            ) : (
                <EmptyState message="No functions" compact />
            )}
        </InspectCard>
    );
};
