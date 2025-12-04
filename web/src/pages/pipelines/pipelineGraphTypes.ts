import type { TBlock, TBlockId } from '@gravity-ui/graph';
import type { FunctionId } from '../../api/pipelines';

export const PIPELINE_BLOCK_IS = 'PipelineBlock';

export const FUNCTION_BLOCK_WIDTH = 200;
export const TERMINAL_BLOCK_WIDTH = 80;
export const BLOCK_HEIGHT = 100;
export const BLOCK_SPACING = FUNCTION_BLOCK_WIDTH / 3;
export const PROTECTED_BLOCK_IDS: TBlockId[] = ['input', 'output'];
export const ANIMATION_DURATION = 400;

export interface BlockLayout {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface TPipelineBlock extends TBlock {
    is: typeof PIPELINE_BLOCK_IS;
    name: string;
    meta: {
        description: string;
        kind?: 'terminal' | 'function';
        functionId?: FunctionId;
    };
}
