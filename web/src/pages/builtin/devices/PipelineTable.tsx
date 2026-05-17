import React, { useCallback, useMemo } from 'react';
import { IconPlus, IconTrash } from './components/Icons';
import type { DevicePipeline } from '../../../api/devices';
import type { PipelineId } from '../../../api/pipelines';

export interface PipelineTableProps {
    pipelineLabel: string;
    pipelines: DevicePipeline[];
    availablePipelines: PipelineId[];
    loadingPipelines?: boolean;
    color?: string;
    onChange: (pipelines: DevicePipeline[]) => void;
}

const parseWeight = (weight: string | number | undefined): number => {
    if (weight === undefined) return 0;
    if (typeof weight === 'number') return weight;
    return parseInt(weight, 10) || 0;
};

export const PipelineTable: React.FC<PipelineTableProps> = ({
    pipelineLabel,
    pipelines,
    availablePipelines,
    loadingPipelines = false,
    color = 'var(--teal)',
    onChange,
}) => {
    const pipelineOptions = useMemo(() => (
        availablePipelines.filter(p => p.name).map(p => p.name || '')
    ), [availablePipelines]);

    const handlePipelineChange = useCallback((index: number, value: string) => {
        const next = [...pipelines];
        next[index] = { ...next[index], name: value };
        onChange(next);
    }, [pipelines, onChange]);

    const handleWeightChange = useCallback((index: number, raw: string) => {
        if (raw === '') {
            const next = [...pipelines];
            next[index] = { ...next[index], weight: 0 };
            onChange(next);
            return;
        }
        // Reject if the string contains a decimal point or is not a valid integer.
        if (raw.includes('.') || raw.includes('e') || raw.includes('E')) {
            return;
        }
        const n = parseInt(raw, 10);
        if (isNaN(n)) {
            return;
        }
        const next = [...pipelines];
        next[index] = { ...next[index], weight: Math.max(0, n) };
        onChange(next);
    }, [pipelines, onChange]);

    const handleRemove = useCallback((index: number) => {
        onChange(pipelines.filter((_, i) => i !== index));
    }, [pipelines, onChange]);

    const handleAdd = useCallback(() => {
        const firstName = pipelineOptions[0] || '';
        onChange([...pipelines, { name: firstName, weight: 1 }]);
    }, [pipelines, pipelineOptions, onChange]);

    return (
        <div className="dv-pipe-col">
            <div className="dv-pipe-hd">
                <span className="dv-pipe-hd-title">{pipelineLabel}</span>
                <span className="dv-pipe-hd-weight">Weight</span>
                <button
                    className="dv-pipe-add"
                    onClick={handleAdd}
                    disabled={loadingPipelines || pipelineOptions.length === 0}
                >
                    <IconPlus size={12} /> Add
                </button>
            </div>

            {pipelines.length === 0 ? (
                <div className="dv-pipe-empty">No {pipelineLabel.toLowerCase()} pipelines attached.</div>
            ) : (
                pipelines.map((pipe, idx) => (
                    <div key={idx} className="dv-pipe-row">
                        <div className="dv-pipe-select">
                            <span className="dv-pipe-tag" style={{ color }}>fn:</span>
                            <select
                                value={pipe.name || ''}
                                onChange={e => handlePipelineChange(idx, e.target.value)}
                                disabled={loadingPipelines}
                            >
                                {pipelineOptions.map(name => (
                                    <option key={name} value={name}>{name}</option>
                                ))}
                                {pipe.name && !pipelineOptions.includes(pipe.name) && (
                                    <option value={pipe.name}>{pipe.name}</option>
                                )}
                            </select>
                        </div>
                        <input
                            className="dv-pipe-weight mono"
                            type="number"
                            min={0}
                            step={1}
                            value={String(parseWeight(pipe.weight))}
                            onChange={e => handleWeightChange(idx, e.target.value)}
                        />
                        <button
                            className="dv-pipe-del"
                            onClick={() => handleRemove(idx)}
                            title="Remove pipeline"
                        >
                            <IconTrash size={13} />
                        </button>
                    </div>
                ))
            )}
        </div>
    );
};
