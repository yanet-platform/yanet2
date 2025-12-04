import type { TBlock, TBlockId } from '@gravity-ui/graph';
import type { ModuleId } from '../../api/functions';

export const GRAVITY_ACTION_BLOCK_IS = 'GravityActionBlock';

export const ACTION_BLOCK_WIDTH = 240;
export const TERMINAL_BLOCK_WIDTH = 80;
export const BLOCK_HEIGHT = 120;
export const BLOCK_SPACING = ACTION_BLOCK_WIDTH / 3;
export const PROTECTED_BLOCK_IDS: TBlockId[] = ['input', 'output'];
export const ANIMATION_DURATION = 400;

export interface BlockLayout {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface TGravityActionBlock extends TBlock {
    is: typeof GRAVITY_ACTION_BLOCK_IS;
    name: string;
    meta: {
        description: string;
        kind?: 'terminal' | 'action';
        moduleId?: ModuleId;
    };
}

