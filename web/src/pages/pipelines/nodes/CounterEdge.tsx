import React from 'react';
import { getBezierPath, BaseEdge, EdgeLabelRenderer } from '@xyflow/react';
import type { EdgeProps } from '@xyflow/react';
import { CounterDisplay, useCounters } from '../../../components';
import { PIPELINE_COUNTER_KEY } from '../hooks';

/**
 * Custom edge component that displays pps/bps counters on the edge.
 * Used for fallthrough pipelines (Input -> Output with no functions).
 */
export const CounterEdge: React.FC<EdgeProps> = ({
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    style,
    markerEnd,
    selected,
}) => {
    const [edgePath, labelX, labelY] = getBezierPath({
        sourceX,
        sourceY,
        sourcePosition,
        targetX,
        targetY,
        targetPosition,
    });

    const { counters } = useCounters();
    const counterData = counters.get(PIPELINE_COUNTER_KEY);
    const loading = counterData === undefined;
    const pps = counterData?.pps ?? 0;
    const bps = counterData?.bps ?? 0;

    // Apply selection styles
    const edgeStyle = {
        ...style,
        strokeWidth: selected ? 3 : 2,
        stroke: selected ? 'var(--g-color-line-brand)' : style?.stroke,
    };

    // Position label below the edge (offset by 20px)
    const labelOffset = 20;

    return (
        <>
            <BaseEdge id={id} path={edgePath} style={edgeStyle} markerEnd={markerEnd} />
            <EdgeLabelRenderer>
                <div
                    className="counter-edge-label"
                    style={{
                        transform: `translate(-50%, 0) translate(${labelX}px, ${labelY + labelOffset}px)`,
                        pointerEvents: 'none',
                    }}
                >
                    <CounterDisplay pps={pps} bps={bps} loading={loading} />
                </div>
            </EdgeLabelRenderer>
        </>
    );
};

CounterEdge.displayName = 'CounterEdge';
