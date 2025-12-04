import type { TAnchor, TConnection } from '@gravity-ui/graph';
import { EAnchorType } from '@gravity-ui/graph';
import type { FunctionId } from '../../api/pipelines';
import {
    PIPELINE_BLOCK_IS,
    FUNCTION_BLOCK_WIDTH,
    TERMINAL_BLOCK_WIDTH,
    BLOCK_HEIGHT,
    BLOCK_SPACING,
} from './pipelineGraphTypes';
import type { TPipelineBlock } from './pipelineGraphTypes';

export const createInputBlock = (x: number, y: number): TPipelineBlock => {
    return {
        id: 'input',
        is: PIPELINE_BLOCK_IS,
        x,
        y,
        width: TERMINAL_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: '',
        meta: {
            description: '',
            kind: 'terminal',
        },
        anchors: [
            {
                id: 'input-out',
                blockId: 'input',
                type: EAnchorType.OUT,
                x: TERMINAL_BLOCK_WIDTH,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

export const createOutputBlock = (x: number, y: number): TPipelineBlock => {
    return {
        id: 'output',
        is: PIPELINE_BLOCK_IS,
        x,
        y,
        width: TERMINAL_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: '',
        meta: {
            description: 'Pipeline output',
            kind: 'terminal',
        },
        anchors: [
            {
                id: 'output-in',
                blockId: 'output',
                type: EAnchorType.IN,
                x: 0,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

export const createFunctionBlock = (x: number, y: number, functionId: FunctionId, index: number): TPipelineBlock => {
    const id = `function-${index}`;
    const displayName = functionId.name || 'Unknown';

    return {
        id,
        is: PIPELINE_BLOCK_IS,
        x,
        y,
        width: FUNCTION_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: displayName,
        meta: {
            description: 'function',
            kind: 'function',
            functionId,
        },
        anchors: [
            {
                id: `${id}-in`,
                blockId: id,
                type: EAnchorType.IN,
                x: 0,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
            {
                id: `${id}-out`,
                blockId: id,
                type: EAnchorType.OUT,
                x: FUNCTION_BLOCK_WIDTH,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

export const createNewFunctionBlock = (x: number, y: number, id: string): TPipelineBlock => {
    return {
        id,
        is: PIPELINE_BLOCK_IS,
        x,
        y,
        width: FUNCTION_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: 'New Function',
        meta: {
            description: 'function',
            kind: 'function',
        },
        anchors: [
            {
                id: `${id}-in`,
                blockId: id,
                type: EAnchorType.IN,
                x: 0,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
            {
                id: `${id}-out`,
                blockId: id,
                type: EAnchorType.OUT,
                x: FUNCTION_BLOCK_WIDTH,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

/**
 * Build initial blocks and connections from functions array (linear pipeline)
 */
export const buildInitialEntities = (functions: FunctionId[]): {
    blocks: TPipelineBlock[];
    connections: TConnection[];
} => {
    const blocks: TPipelineBlock[] = [];
    const connections: TConnection[] = [];

    // Create input block
    blocks.push(createInputBlock(0, 0));

    // Handle empty pipeline case
    if (functions.length === 0) {
        // Create output block right after input
        blocks.push(createOutputBlock(TERMINAL_BLOCK_WIDTH + BLOCK_SPACING, 0));
        // Direct connection: input -> output
        connections.push({
            sourceBlockId: 'input',
            sourceAnchorId: 'input-out',
            targetBlockId: 'output',
            targetAnchorId: 'output-in',
        });
        return { blocks, connections };
    }

    // Create function blocks in a single line
    let x = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;
    functions.forEach((functionId, index) => {
        blocks.push(createFunctionBlock(x, 0, functionId, index));
        x += FUNCTION_BLOCK_WIDTH + BLOCK_SPACING;
    });

    // Create output block at the end
    blocks.push(createOutputBlock(x, 0));

    // Create connections: input -> function[0]
    const firstFunctionId = 'function-0';
    connections.push({
        sourceBlockId: 'input',
        sourceAnchorId: 'input-out',
        targetBlockId: firstFunctionId,
        targetAnchorId: `${firstFunctionId}-in`,
    });

    // function[i] -> function[i+1]
    for (let i = 0; i < functions.length - 1; i++) {
        const fromId = `function-${i}`;
        const toId = `function-${i + 1}`;
        connections.push({
            sourceBlockId: fromId,
            sourceAnchorId: `${fromId}-out`,
            targetBlockId: toId,
            targetAnchorId: `${toId}-in`,
        });
    }

    // function[last] -> output
    const lastFunctionId = `function-${functions.length - 1}`;
    connections.push({
        sourceBlockId: lastFunctionId,
        sourceAnchorId: `${lastFunctionId}-out`,
        targetBlockId: 'output',
        targetAnchorId: 'output-in',
    });

    return { blocks, connections };
};
