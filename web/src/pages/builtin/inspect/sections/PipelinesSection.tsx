import React, { useMemo } from 'react';
import type { InstanceInfo } from '../../../../api/inspect';
import { InspectCard } from '../InspectCard';
import { PipelineRow } from '../PipelineRow';
import { EmptyState } from '../../../../components';
import { usePipelineCounters } from '../hooks';

export interface PipelinesSectionProps {
    instance: InstanceInfo;
}

export const PipelinesSection: React.FC<PipelinesSectionProps> = ({ instance }) => {
    const pipelines = instance.pipelines ?? [];

    const deviceNames = useMemo(
        () => (instance.devices ?? []).map((d, idx) => d.name ?? `device-${idx}`),
        [instance.devices],
    );
    const pipelineNames = useMemo(
        () => pipelines.map((p, idx) => p.name ?? `pipeline-${idx}`),
        [pipelines],
    );

    const { rates, series } = usePipelineCounters(
        deviceNames,
        pipelineNames,
        pipelines.length > 0,
    );

    return (
        <InspectCard title="Pipelines" count={pipelines.length}>
            {pipelines.length > 0 ? (
                <div className="inspect-pipe-list">
                    {pipelines.map((p, idx) => {
                        const name = p.name ?? `pipeline-${idx}`;
                        const rate = rates.get(name);
                        const ser = series.get(name) ?? [];
                        return (
                            <PipelineRow
                                key={name}
                                name={name}
                                functions={p.functions ?? []}
                                pps={rate?.pps ?? 0}
                                series={ser}
                            />
                        );
                    })}
                </div>
            ) : (
                <EmptyState message="No pipelines" compact />
            )}
        </InspectCard>
    );
};
