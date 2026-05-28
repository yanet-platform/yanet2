import type { Pipeline, PipelinesAction } from './types';
import { localToApi } from './wire';
import type { EntityState, BaseEntityAction } from '../_shared/editableEntityStore';
import {
    createInitialEntityState,
    applyEntityUpdate,
    handleBaseEntityAction,
} from '../_shared/editableEntityStore';

export type PipelinesState = EntityState<Pipeline>;

export const initialState: PipelinesState = createInitialEntityState<Pipeline>();

const findPipeline = (state: PipelinesState, pipelineId: string): Pipeline | undefined =>
    state.local[pipelineId];

const updatePipeline = (state: PipelinesState, pipelineId: string, updated: Pipeline): PipelinesState =>
    applyEntityUpdate(state, pipelineId, updated, localToApi);

export const pipelinesReducer = (
    state: PipelinesState,
    action: PipelinesAction | BaseEntityAction<Pipeline>,
): PipelinesState => {
    switch (action.type) {
        case 'LOAD_ENTITY':
        case 'ADD_ENTITY':
        case 'REMOVE_ENTITY':
            return handleBaseEntityAction(state, action as BaseEntityAction<Pipeline>);

        case 'MOVE_FUNCTION_REF': {
            const fromPipeline = findPipeline(state, action.fromPipelineId);
            const toPipeline = findPipeline(state, action.toPipelineId);
            if (!fromPipeline || !toPipeline) {
                return state;
            }
            const fromIdx = fromPipeline.functions.findIndex(r => r.id === action.refId);
            if (fromIdx === -1) {
                return state;
            }

            if (action.fromPipelineId === action.toPipelineId) {
                const toIdx = action.toIdx;
                if (fromIdx === toIdx || fromIdx === toIdx - 1) {
                    return state;
                }
                const refs = [...fromPipeline.functions];
                const [moved] = refs.splice(fromIdx, 1);
                const insertAt = fromIdx < toIdx ? toIdx - 1 : toIdx;
                refs.splice(insertAt, 0, moved);
                return updatePipeline(state, action.fromPipelineId, { ...fromPipeline, functions: refs });
            }

            const moved = fromPipeline.functions[fromIdx];
            const sourceRefs = [...fromPipeline.functions];
            sourceRefs.splice(fromIdx, 1);
            const targetRefs = [...toPipeline.functions];
            targetRefs.splice(action.toIdx, 0, moved);

            const sourceNext = { ...fromPipeline, functions: sourceRefs };
            const targetNext = { ...toPipeline, functions: targetRefs };
            const sourceState = applyEntityUpdate(state, action.fromPipelineId, sourceNext, localToApi);
            return applyEntityUpdate(sourceState, action.toPipelineId, targetNext, localToApi);
        }

        case 'ADD_FUNCTION_REF': {
            const pl = findPipeline(state, action.pipelineId);
            if (!pl) {
                return state;
            }
            const refs = [...pl.functions];
            refs.splice(action.toIdx, 0, action.ref);
            return updatePipeline(state, action.pipelineId, { ...pl, functions: refs });
        }

        case 'REMOVE_FUNCTION_REF': {
            const pl = findPipeline(state, action.pipelineId);
            if (!pl) {
                return state;
            }
            const updated: Pipeline = {
                ...pl,
                functions: pl.functions.filter(r => r.id !== action.refId),
            };
            return updatePipeline(state, action.pipelineId, updated);
        }

        case 'UPDATE_FUNCTION_REF': {
            const pl = findPipeline(state, action.pipelineId);
            if (!pl) {
                return state;
            }
            const updated: Pipeline = {
                ...pl,
                functions: pl.functions.map(r =>
                    r.id === action.refId ? { ...r, name: action.name } : r
                ),
            };
            return updatePipeline(state, action.pipelineId, updated);
        }

        default:
            return state;
    }
};
