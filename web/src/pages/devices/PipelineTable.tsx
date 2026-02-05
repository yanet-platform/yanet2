import React, { useCallback, useMemo } from 'react';
import { Box, TextInput, Button, Select } from '@gravity-ui/uikit';
import { Plus, TrashBin } from '@gravity-ui/icons';
import type { DevicePipeline } from '../../api/devices';
import type { PipelineId } from '../../api/pipelines';
import './devices.scss';

export interface PipelineTableProps {
    pipelines: DevicePipeline[];
    availablePipelines: PipelineId[];
    loadingPipelines?: boolean;
    onChange: (pipelines: DevicePipeline[]) => void;
    pipelineLabel?: string;
}

const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

export const PipelineTable: React.FC<PipelineTableProps> = ({
    pipelines,
    availablePipelines,
    loadingPipelines = false,
    onChange,
    pipelineLabel = 'Pipeline',
}) => {
    const pipelineOptions = useMemo(() => {
        return availablePipelines
            .filter(p => p.name)
            .map(p => ({
                value: p.name || '',
                content: p.name || '',
            }));
    }, [availablePipelines]);

    const handlePipelineChange = useCallback((index: number, value: string[]) => {
        if (value.length === 0) return;
        const newPipelines = [...pipelines];
        newPipelines[index] = {
            ...newPipelines[index],
            name: value[0],
        };
        onChange(newPipelines);
    }, [pipelines, onChange]);

    const handleWeightChange = useCallback((index: number, value: string) => {
        const newWeight = parseInt(value, 10);
        if (isNaN(newWeight) && value !== '') return;

        const newPipelines = [...pipelines];
        newPipelines[index] = {
            ...newPipelines[index],
            weight: value === '' ? 0 : newWeight,
        };
        onChange(newPipelines);
    }, [pipelines, onChange]);

    const handleRemove = useCallback((index: number) => {
        const newPipelines = pipelines.filter((_, i) => i !== index);
        onChange(newPipelines);
    }, [pipelines, onChange]);

    const handleAdd = useCallback(() => {
        const firstAvailable = availablePipelines[0]?.name || '';
        const newPipeline: DevicePipeline = {
            name: firstAvailable,
            weight: 1,
        };
        onChange([...pipelines, newPipeline]);
    }, [pipelines, availablePipelines, onChange]);

    return (
        <Box className="pipeline-table">
            <table className="pipeline-table__table">
                <thead>
                    <tr className="pipeline-table__header-row">
                        <th className="pipeline-table__header-cell">
                            {pipelineLabel}
                        </th>
                        <th className="pipeline-table__header-cell pipeline-table__header-cell--weight">
                            Weight
                        </th>
                        <th className="pipeline-table__header-cell pipeline-table__header-cell--actions">
                            <Button
                                view="flat"
                                size="s"
                                onClick={handleAdd}
                                disabled={loadingPipelines || pipelineOptions.length === 0}
                            >
                                <Button.Icon>
                                    <Plus />
                                </Button.Icon>
                                Add
                            </Button>
                        </th>
                    </tr>
                </thead>
                <tbody>
                    {pipelines.length === 0 ? (
                        <tr>
                            <td colSpan={3} className="pipeline-table__empty-cell">
                                No pipelines configured
                            </td>
                        </tr>
                    ) : (
                        pipelines.map((pipeline, index) => (
                            <tr key={index} className="pipeline-table__body-row">
                                <td className="pipeline-table__body-cell">
                                    <Select
                                        value={pipeline.name ? [pipeline.name] : []}
                                        options={pipelineOptions}
                                        onUpdate={(value) => handlePipelineChange(index, value)}
                                        filterable
                                        width="max"
                                        disabled={loadingPipelines}
                                    />
                                </td>
                                <td className="pipeline-table__body-cell">
                                    <TextInput
                                        value={String(parseWeight(pipeline.weight))}
                                        onChange={(e) => handleWeightChange(index, e.target.value)}
                                        size="m"
                                        type="number"
                                    />
                                </td>
                                <td className="pipeline-table__body-cell pipeline-table__body-cell--actions">
                                    <Button
                                        view="flat-danger"
                                        size="s"
                                        onClick={() => handleRemove(index)}
                                    >
                                        <Button.Icon>
                                            <TrashBin />
                                        </Button.Icon>
                                    </Button>
                                </td>
                            </tr>
                        ))
                    )}
                </tbody>
            </table>
        </Box>
    );
};
