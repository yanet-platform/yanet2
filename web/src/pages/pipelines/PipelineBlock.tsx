import React from 'react';
import { Flex, Text, Icon } from '@gravity-ui/uikit';
import type { Graph, TBlock } from '@gravity-ui/graph';
import { GraphBlock, GraphBlockAnchor, useBlockState } from '@gravity-ui/graph/react';
import type { FunctionId } from '../../api/pipelines';
import { ArrowRightFromSquare, ArrowRightToSquare } from '@gravity-ui/icons';
import './PipelineBlock.css';

interface TPipelineBlock extends TBlock {
    is: 'PipelineBlock';
    name: string;
    meta: {
        description: string;
        kind?: 'terminal' | 'function';
        functionId?: FunctionId;
    };
}

interface PipelineBlockProps {
    graph: Graph;
    block: TPipelineBlock;
    onDoubleClick?: (blockId: string) => void;
}

export const PipelineBlock = ({ graph, block, onDoubleClick }: PipelineBlockProps): React.JSX.Element => {
    const blockState = useBlockState(graph, block.id);
    const anchorStates = blockState?.$anchorStates?.value;
    const isTerminalBlock = block.meta?.kind === 'terminal' || block.id === 'input' || block.id === 'output';
    const isInputBlock = block.id === 'input';
    const wrapperClassName = ['pipeline-block-wrapper', isTerminalBlock ? 'pipeline-block-terminal' : '']
        .filter(Boolean)
        .join(' ');

    const handleDoubleClick = (e: React.MouseEvent) => {
        e.stopPropagation();
        // Allow double-click on function blocks only (not terminal blocks)
        if (onDoubleClick && !isTerminalBlock) {
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

                    return (
                        <GraphBlockAnchor
                            className="pipeline-block-anchor"
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
                    style={{ cursor: !isTerminalBlock ? 'pointer' : 'default' }}
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
                        className="pipeline-block-name"
                        style={{ textAlign: 'center' }}
                    >
                        {block.name}
                    </Text>
                    {block.meta.description && (
                        <Text
                            as="div"
                            ellipsis
                            variant="caption-1"
                            color="secondary"
                            style={{ textAlign: 'center' }}
                        >
                            {block.meta.description}
                        </Text>
                    )}
                    {!isTerminalBlock && (
                        <Text
                            as="div"
                            variant="caption-2"
                            color="hint"
                            style={{ textAlign: 'center', marginTop: '4px' }}
                        >
                            Double-click to edit
                        </Text>
                    )}
                </Flex>
            </div>
        </GraphBlock>
    );
};
