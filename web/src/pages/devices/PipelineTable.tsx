import React, { useCallback, useMemo } from 'react';
import { Box, Text, TextInput, Button, Flex, Select, Table, withTableSelection } from '@gravity-ui/uikit';
import { Plus, TrashBin } from '@gravity-ui/icons';
import type { DevicePipeline } from '../../api/devices';
import type { PipelineId } from '../../api/pipelines';

export interface PipelineTableProps {
    title: string;
    pipelines: DevicePipeline[];
    availablePipelines: PipelineId[];
    loadingPipelines?: boolean;
    onChange: (pipelines: DevicePipeline[]) => void;
}

const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

export const PipelineTable: React.FC<PipelineTableProps> = ({
    title,
    pipelines,
    availablePipelines,
    loadingPipelines = false,
    onChange,
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
        <Box
            style={{
                border: '1px solid var(--g-color-line-generic)',
                borderRadius: '8px',
                overflow: 'hidden',
            }}
        >
            {/* Header */}
            <Flex
                alignItems="center"
                justifyContent="space-between"
                style={{
                    padding: '8px 12px',
                    backgroundColor: 'var(--g-color-base-generic)',
                    borderBottom: '1px solid var(--g-color-line-generic)',
                }}
            >
                <Text variant="subheader-1">{title}</Text>
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
            </Flex>

            {/* Table content */}
            <Box style={{ padding: '0' }}>
                {pipelines.length === 0 ? (
                    <Text
                        variant="body-1"
                        color="secondary"
                        style={{ padding: '12px', display: 'block', textAlign: 'center' }}
                    >
                        No pipelines configured
                    </Text>
                ) : (
                    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                        <thead>
                            <tr style={{ backgroundColor: 'var(--g-color-base-generic-hover)' }}>
                                <th style={{ 
                                    padding: '8px 12px', 
                                    textAlign: 'left',
                                    fontWeight: 500,
                                    fontSize: '13px',
                                    color: 'var(--g-color-text-secondary)',
                                }}>
                                    Pipeline
                                </th>
                                <th style={{ 
                                    padding: '8px 12px', 
                                    textAlign: 'left',
                                    fontWeight: 500,
                                    fontSize: '13px',
                                    color: 'var(--g-color-text-secondary)',
                                    width: '120px',
                                }}>
                                    Weight
                                </th>
                                <th style={{ 
                                    padding: '8px 12px', 
                                    textAlign: 'center',
                                    width: '50px',
                                }}>
                                </th>
                            </tr>
                        </thead>
                        <tbody>
                            {pipelines.map((pipeline, index) => (
                                <tr 
                                    key={index}
                                    style={{ 
                                        borderTop: '1px solid var(--g-color-line-generic)',
                                    }}
                                >
                                    <td style={{ padding: '6px 12px' }}>
                                        <Select
                                            value={pipeline.name ? [pipeline.name] : []}
                                            options={pipelineOptions}
                                            onUpdate={(value) => handlePipelineChange(index, value)}
                                            filterable
                                            width="max"
                                            disabled={loadingPipelines}
                                        />
                                    </td>
                                    <td style={{ padding: '6px 12px' }}>
                                        <TextInput
                                            value={String(parseWeight(pipeline.weight))}
                                            onChange={(e) => handleWeightChange(index, e.target.value)}
                                            size="m"
                                            type="number"
                                        />
                                    </td>
                                    <td style={{ padding: '6px 12px', textAlign: 'center' }}>
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
                            ))}
                        </tbody>
                    </table>
                )}
            </Box>
        </Box>
    );
};

