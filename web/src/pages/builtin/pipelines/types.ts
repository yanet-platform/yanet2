export type { DragPayload } from '../_shared/lane-editor';

/** A reference to a globally-named function inside a pipeline track. */
export interface FunctionRef {
    /** Synthetic stable id derived from pipeline name + position + function name. */
    id: string;
    /** The referenced function name (FunctionId.name). May be empty for a freshly-added ref. */
    name: string;
}

export interface Pipeline {
    id: string;
    functions: FunctionRef[];
}

export type PipelinesAction =
    | { type: 'MOVE_FUNCTION_REF';   pipelineId: string; refId: string; toIdx: number }
    | { type: 'ADD_FUNCTION_REF';    pipelineId: string; toIdx: number; ref: FunctionRef }
    | { type: 'REMOVE_FUNCTION_REF'; pipelineId: string; refId: string }
    | { type: 'UPDATE_FUNCTION_REF'; pipelineId: string; refId: string; name: string };
