import React, { memo } from 'react';
import { Handle, Position } from '@xyflow/react';
import type { NodeProps, Node } from '@xyflow/react';
import { Text } from '@gravity-ui/uikit';
import type { ModuleNodeData } from '../types';

type ModuleNodeProps = NodeProps<Node<ModuleNodeData>>;

export const ModuleNode: React.FC<ModuleNodeProps> = memo(({ data, selected }) => {
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
