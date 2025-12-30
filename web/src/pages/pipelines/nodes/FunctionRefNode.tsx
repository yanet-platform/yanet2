import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Text } from '@gravity-ui/uikit';
import { CounterDisplay } from '../../../components';
import type { FunctionRefNodeData } from '../types';
import { useCounters } from '../CountersContext';

type FunctionRefNodeProps = NodeProps<Node<FunctionRefNodeData>>;

// Inner component that subscribes to counters context
// Separated to allow memo optimization on the outer component while still
// receiving context updates
const FunctionCounterDisplay: React.FC<{ functionName: string }> = ({ functionName }) => {
    const { counters } = useCounters();
    const counterData = counters.get(functionName);
    const pps = counterData?.pps ?? 0;
    const bps = counterData?.bps ?? 0;

    return (
        <div className="function-ref-node__counters">
            <CounterDisplay pps={pps} bps={bps} />
        </div>
    );
};

export const FunctionRefNode: React.FC<FunctionRefNodeProps> = memo(({ data, selected }) => {
    const nodeData = data as FunctionRefNodeData;

    return (
        <div className={`function-ref-node${selected ? ' selected' : ''}`}>
            <Handle
                type="target"
                position={Position.Left}
                className="node-handle"
            />
            <div className="function-ref-node__content">
                <div className="function-ref-node__row">
                    <span className="function-ref-node__label">Function</span>
                </div>
                <div className="function-ref-node__row">
                    <Text variant="body-1" ellipsis>
                        {nodeData.functionName || '—'}
                    </Text>
                </div>
                <FunctionCounterDisplay functionName={nodeData.functionName} />
            </div>
            <Handle
                type="source"
                position={Position.Right}
                className="node-handle"
            />
        </div>
    );
});

FunctionRefNode.displayName = 'FunctionRefNode';
