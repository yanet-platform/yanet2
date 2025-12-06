import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Icon } from '@gravity-ui/uikit';
import { ArrowRight } from '@gravity-ui/icons';
import type { OutputNodeData } from '../types';

type OutputNodeProps = NodeProps<Node<OutputNodeData>>;

export const OutputNode: React.FC<OutputNodeProps> = memo(({ selected }) => {
    return (
        <div className={`output-node${selected ? ' selected' : ''}`}>
            <Icon data={ArrowRight} size={20} />
            <Handle
                type="target"
                position={Position.Left}
                className="node-handle"
            />
        </div>
    );
});

OutputNode.displayName = 'OutputNode';
