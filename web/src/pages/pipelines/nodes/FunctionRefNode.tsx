import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Text } from '@gravity-ui/uikit';
import type { FunctionRefNodeData } from '../types';

type FunctionRefNodeProps = NodeProps<Node<FunctionRefNodeData>>;

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
