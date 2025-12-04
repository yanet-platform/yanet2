import React from 'react';
import { Flex, Text, Icon } from '@gravity-ui/uikit';
import type { Graph, TBlock } from '@gravity-ui/graph';
import { GraphBlock, GraphBlockAnchor, useBlockState } from '@gravity-ui/graph/react';
import type { ModuleId } from '../../api/functions';
import { ArrowRightFromSquare, ArrowRightToSquare } from '@gravity-ui/icons';
import './ActionBlock.css';

interface TGravityActionBlock extends TBlock {
    is: 'GravityActionBlock';
    name: string;
    meta: {
        description: string;
        kind?: 'terminal' | 'action';
        moduleId?: ModuleId;
    };
}

interface ActionBlockProps {
    graph: Graph;
    block: TGravityActionBlock;
    onDoubleClick?: (blockId: string) => void;
}

export const ActionBlock = ({ graph, block, onDoubleClick }: ActionBlockProps): React.JSX.Element => {
    const blockState = useBlockState(graph, block.id);
    const anchorStates = blockState?.$anchorStates?.value;
    const isTerminalBlock = block.meta?.kind === 'terminal' || block.id === 'input' || block.id === 'output';
    const isInputBlock = block.id === 'input';
    const wrapperClassName = ['action-block-wrapper', isTerminalBlock ? 'action-block-terminal' : '']
        .filter(Boolean)
        .join(' ');

    const handleDoubleClick = (e: React.MouseEvent) => {
        e.stopPropagation();
        // Allow double-click on INPUT block (for weight editing) and action blocks
        if (onDoubleClick && (isInputBlock || !isTerminalBlock)) {
            onDoubleClick(block.id as string);
        }
    };

    return (
        <GraphBlock graph={graph} block={block} className={wrapperClassName}>
            <div
                style={{ display: 'contents' }}
                onDoubleClick={handleDoubleClick}
            >
                {block.anchors?.map((anchor) => {
                    // Check if anchor is initialized in graph state
                    const anchorState = anchorStates?.find((a) => a.id === anchor.id);
                    if (!anchorState) return null;

                    // Use anchor from block (which now has blockId) for GraphBlockAnchor
                    return (
                        <GraphBlockAnchor
                            className="action-block-anchor"
                            key={anchor.id}
                            position="absolute"
                            graph={graph}
                            anchor={{
                                id: anchor.id,
                                blockId: block.id,
                                type: anchor.type,
                            }}
                        />
                    );
                })}
                <Flex
                    grow={1}
                    direction="column"
                    justifyContent="center"
                    alignItems="center"
                    gap={isTerminalBlock ? 2 : 1}
                    style={{ cursor: (isInputBlock || !isTerminalBlock) ? 'pointer' : 'default' }}
                >
                    {isTerminalBlock && (
                        <Icon
                            data={isInputBlock ? ArrowRightFromSquare : ArrowRightToSquare}
                            size={28}
                            style={{ color: 'var(--g-color-text-primary)' }}
                        />
                    )}
                    <Text
                        as="div"
                        ellipsis
                        variant={isTerminalBlock ? 'body-2' : 'subheader-1'}
                        className="action-block-name"
                        style={{ textAlign: 'center' }}
                    >
                        {block.name}
                    </Text>
                    {block.meta.description && (
                        <Text
                            as="div"
                            ellipsis
                            variant={isTerminalBlock ? 'caption-1' : 'caption-1'}
                            color="secondary"
                            style={{ textAlign: 'center' }}
                        >
                            {block.meta.description}
                        </Text>
                    )}
                    {(!isTerminalBlock || isInputBlock) && (
                        <Text
                            as="div"
                            variant="caption-2"
                            color="hint"
                            style={{ textAlign: 'center', marginTop: '4px' }}
                        >
                            {isInputBlock ? 'Double-click for chains' : 'Double-click to edit'}
                        </Text>
                    )}
                </Flex>
            </div>
        </GraphBlock>
    );
}
