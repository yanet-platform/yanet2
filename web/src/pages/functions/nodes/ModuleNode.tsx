import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Text } from '@gravity-ui/uikit';
import { CounterDisplay, useCounters } from '../../../components';
import type { ModuleNodeData } from '../types';

type ModuleNodeProps = NodeProps<Node<ModuleNodeData>>;

// Inner component that subscribes to counters context
// Separated to allow memo optimization on the outer component while still
// receiving context updates
const ModuleCounterDisplay: React.FC<{ moduleKey: string }> = ({ moduleKey }) => {
    const { counters } = useCounters();
    const counterData = counters.get(moduleKey);
    const pps = counterData?.pps ?? 0;
    const bps = counterData?.bps ?? 0;

    return (
        <div className="module-node__counters">
            <CounterDisplay pps={pps} bps={bps} />
        </div>
    );
};

export const ModuleNode: React.FC<ModuleNodeProps> = memo(({ id, data, selected }) => {
    const nodeData = data as ModuleNodeData;

    return (
        <div className={`module-node${selected ? ' selected' : ''}`}>
            <Handle
                type="target"
                position={Position.Left}
                className="node-handle"
            />
            <div className="module-node__content">
                <div className="module-node__row">
                    <span className="module-node__label">Type</span>
                    <span className="module-node__separator" />
                    <Text variant="body-1" ellipsis>
                        {nodeData.type || '—'}
                    </Text>
                </div>
                <div className="module-node__row">
                    <span className="module-node__label">Name</span>
                    <span className="module-node__separator" />
                    <Text variant="body-1" ellipsis>
                        {nodeData.name || '—'}
                    </Text>
                </div>
                <ModuleCounterDisplay moduleKey={id} />
            </div>
            <Handle
                type="source"
                position={Position.Right}
                className="node-handle"
            />
        </div>
    );
});

ModuleNode.displayName = 'ModuleNode';
