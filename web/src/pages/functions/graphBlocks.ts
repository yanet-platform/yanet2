import type { TAnchor, TConnection, TBlockId } from '@gravity-ui/graph';
import { EAnchorType } from '@gravity-ui/graph';
import type { ModuleId } from '../../api/functions';
import {
    GRAVITY_ACTION_BLOCK_IS,
    ACTION_BLOCK_WIDTH,
    TERMINAL_BLOCK_WIDTH,
    BLOCK_HEIGHT,
    BLOCK_SPACING,
} from './graphTypes';
import type { TGravityActionBlock } from './graphTypes';
import type { ChainPath } from './Graph';

export const createInputBlock = (x: number, y: number): TGravityActionBlock => {
    return {
        id: 'input',
        is: GRAVITY_ACTION_BLOCK_IS,
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

export const createOutputBlock = (x: number, y: number): TGravityActionBlock => {
    return {
        id: 'output',
        is: GRAVITY_ACTION_BLOCK_IS,
        x,
        y,
        width: TERMINAL_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: '',
        meta: {
            description: 'Packet output',
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

export const createModuleBlock = (x: number, y: number, moduleId: ModuleId, index: number): TGravityActionBlock => {
    const id = `module-${index}`;
    const displayName = moduleId.name || 'Unknown';
    const typeName = moduleId.type || 'module';

    return {
        id,
        is: GRAVITY_ACTION_BLOCK_IS,
        x,
        y,
        width: ACTION_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: displayName,
        meta: {
            description: typeName,
            kind: 'action',
            moduleId,
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
                x: ACTION_BLOCK_WIDTH,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

export const createNewActionBlock = (x: number, y: number, id: string): TGravityActionBlock => {
    return {
        id,
        is: GRAVITY_ACTION_BLOCK_IS,
        x,
        y,
        width: ACTION_BLOCK_WIDTH,
        height: BLOCK_HEIGHT,
        name: 'New Module',
        meta: {
            description: 'module',
            kind: 'action',
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
                x: ACTION_BLOCK_WIDTH,
                y: BLOCK_HEIGHT / 2,
            } as TAnchor & { x: number; y: number },
        ],
    };
};

/**
 * Build initial blocks and connections from chains array
 */
export const buildInitialEntities = (chains: ChainPath[]): {
    blocks: TGravityActionBlock[];
    connections: TConnection[];
    chainWeights: Map<TBlockId, number>;
} => {
    const blocks: TGravityActionBlock[] = [];
    const connections: TConnection[] = [];
    const chainWeights = new Map<TBlockId, number>();

    // Create input block
    blocks.push(createInputBlock(0, 0));

    // Track module index globally across all chains
    let moduleIndex = 0;
    // Calculate vertical offset for each chain
    const chainSpacingY = BLOCK_HEIGHT + BLOCK_SPACING;

    // Handle empty chains case
    if (chains.length === 0) {
        // Create output block
        blocks.push(createOutputBlock(TERMINAL_BLOCK_WIDTH + BLOCK_SPACING, 0));
        // Direct connection: input -> output
        connections.push({
            sourceBlockId: 'input',
            sourceAnchorId: 'input-out',
            targetBlockId: 'output',
            targetAnchorId: 'output-in',
        });
        return { blocks, connections, chainWeights };
    }

    // Find max chain length to position output block
    const maxChainLength = Math.max(...chains.map(c => c.modules.length), 0);
    const outputX = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING + (maxChainLength > 0 ? (ACTION_BLOCK_WIDTH + BLOCK_SPACING) * maxChainLength : 0);

    // Calculate center Y for all chains
    const totalHeight = chains.length * chainSpacingY - BLOCK_SPACING;
    const startY = -totalHeight / 2 + BLOCK_HEIGHT / 2;

    // Create blocks and connections for each chain
    chains.forEach((chain, chainIndex) => {
        const chainY = startY + chainIndex * chainSpacingY;
        const chainStartModuleIndex = moduleIndex;

        if (chain.modules.length === 0) {
            // Empty chain: input -> output directly
            connections.push({
                sourceBlockId: 'input',
                sourceAnchorId: 'input-out',
                targetBlockId: 'output',
                targetAnchorId: 'output-in',
            });
            // Store weight (use 'output' as key for empty chains, but this is a special case)
            // We'll use a special key format for empty chains
            chainWeights.set(`empty-chain-${chainIndex}`, chain.weight);
        } else {
            // Create module blocks for this chain
            let x = TERMINAL_BLOCK_WIDTH + BLOCK_SPACING;
            chain.modules.forEach((moduleId) => {
                blocks.push(createModuleBlock(x, chainY, moduleId, moduleIndex));
                x += ACTION_BLOCK_WIDTH + BLOCK_SPACING;
                moduleIndex++;
            });

            // input -> first module of this chain
            const firstModuleId = `module-${chainStartModuleIndex}`;
            connections.push({
                sourceBlockId: 'input',
                sourceAnchorId: 'input-out',
                targetBlockId: firstModuleId,
                targetAnchorId: `${firstModuleId}-in`,
            });

            // Store weight by first block ID
            chainWeights.set(firstModuleId, chain.weight);

            // module[i] -> module[i+1] within this chain
            for (let i = 0; i < chain.modules.length - 1; i++) {
                const fromId = `module-${chainStartModuleIndex + i}`;
                const toId = `module-${chainStartModuleIndex + i + 1}`;
                connections.push({
                    sourceBlockId: fromId,
                    sourceAnchorId: `${fromId}-out`,
                    targetBlockId: toId,
                    targetAnchorId: `${toId}-in`,
                });
            }

            // last module -> output
            const lastModuleId = `module-${chainStartModuleIndex + chain.modules.length - 1}`;
            connections.push({
                sourceBlockId: lastModuleId,
                sourceAnchorId: `${lastModuleId}-out`,
                targetBlockId: 'output',
                targetAnchorId: 'output-in',
            });
        }
    });

    // Create output block at the end
    const outputY = chains.length === 1 ? startY : 0;
    blocks.push(createOutputBlock(outputX, outputY));

    return { blocks, connections, chainWeights };
};
