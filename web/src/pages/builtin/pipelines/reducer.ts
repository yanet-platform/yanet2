import type { Pipeline, PipelinesAction } from './types';
import { localToApi } from './wire';

export interface PipelinesState {
    /** Server-authoritative snapshots, keyed by pipeline id. */
    server: Record<string, Pipeline>;
    /** Local edited state, keyed by pipeline id. */
    local: Record<string, Pipeline>;
    /** Which pipeline ids have unsaved edits. */
    dirty: Set<string>;
}

export const initialState: PipelinesState = {
    server: {},
    local: {},
    dirty: new Set(),
};

const findPipeline = (state: PipelinesState, pipelineId: string): Pipeline | undefined =>
    state.local[pipelineId];

/** Returns true when the local pipeline differs from the server snapshot. */
const isDirty = (updated: Pipeline, server: Pipeline | undefined): boolean => {
    if (!server) {
        return true;
    }
    return JSON.stringify(localToApi(updated)) !== JSON.stringify(localToApi(server));
};

const updatePipeline = (state: PipelinesState, pipelineId: string, updated: Pipeline): PipelinesState => {
    const dirty = new Set(state.dirty);
    if (isDirty(updated, state.server[pipelineId])) {
        dirty.add(pipelineId);
    } else {
        dirty.delete(pipelineId);
    }
    return {
        ...state,
        local: { ...state.local, [pipelineId]: updated },
        dirty,
    };
};

export const pipelinesReducer = (state: PipelinesState, action: PipelinesAction): PipelinesState => {
    switch (action.type) {
        case 'LOAD_PIPELINE': {
            const pl = action.pipeline;
            const dirty = new Set(state.dirty);
            dirty.delete(pl.id);
            return {
                ...state,
                server: { ...state.server, [pl.id]: pl },
                local: { ...state.local, [pl.id]: pl },
                dirty,
            };
        }

        case 'MOVE_FUNCTION_REF': {
            const pl = findPipeline(state, action.pipelineId);
            if (!pl) {
                return state;
            }
            const fromIdx = pl.functions.findIndex(r => r.id === action.refId);
            if (fromIdx === -1) {
                return state;
            }
            const toIdx = action.toIdx;
            if (fromIdx === toIdx || fromIdx === toIdx - 1) {
                return state;
            }
            const refs = [...pl.functions];
            const [moved] = refs.splice(fromIdx, 1);
            // proof: fromIdx < toIdx → insert position after removal = toIdx - 1.
            //        fromIdx > toIdx → insert position unchanged = toIdx.
            const insertAt = fromIdx < toIdx ? toIdx - 1 : toIdx;
            refs.splice(insertAt, 0, moved);
            return updatePipeline(state, action.pipelineId, { ...pl, functions: refs });
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

        case 'ADD_PIPELINE': {
            const pl = action.pipeline;
            return {
                ...state,
                server: { ...state.server, [pl.id]: pl },
                local: { ...state.local, [pl.id]: pl },
            };
        }

        case 'REMOVE_PIPELINE': {
            const { [action.pipelineId]: _s, ...serverRest } = state.server;
            const { [action.pipelineId]: _l, ...localRest } = state.local;
            const dirty = new Set(state.dirty);
            dirty.delete(action.pipelineId);
            return { server: serverRest, local: localRest, dirty };
        }

        default:
            return state;
    }
};

/** Mark a pipeline as clean (after successful save). */
export const markClean = (state: PipelinesState, pipelineId: string): PipelinesState => {
    const dirty = new Set(state.dirty);
    dirty.delete(pipelineId);
    return {
        ...state,
        server: { ...state.server, [pipelineId]: state.local[pipelineId] },
        dirty,
    };
};

/** Discard local edits for a pipeline, reverting to server snapshot. */
export const discardEdits = (state: PipelinesState, pipelineId: string): PipelinesState => {
    const server = state.server[pipelineId];
    if (!server) {
        return state;
    }
    const dirty = new Set(state.dirty);
    dirty.delete(pipelineId);
    return {
        ...state,
        local: { ...state.local, [pipelineId]: server },
        dirty,
    };
};
