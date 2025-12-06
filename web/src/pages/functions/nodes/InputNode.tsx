import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Icon } from '@gravity-ui/uikit';
import { ArrowRight } from '@gravity-ui/icons';
import type { InputNodeData } from '../types';

type InputNodeProps = NodeProps<Node<InputNodeData>>;

export const InputNode: React.FC<InputNodeProps> = memo(({ selected }) => {
    return (
        <div className={`input-node${selected ? ' selected' : ''}`}>
            <Icon data={ArrowRight} size={20} />
            <Handle
                type="source"
                position={Position.Right}
                className="node-handle"
            />
        </div>
    );
});

InputNode.displayName = 'InputNode';
