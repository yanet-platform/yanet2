import { describe, expect, it } from 'vitest';
import { pipelinesReducer, initialState } from './reducer';
import type { Pipeline } from './types';

const loadPipeline = (state: typeof initialState, pipeline: Pipeline) =>
    pipelinesReducer(state, { type: 'LOAD_ENTITY', id: pipeline.id, entity: pipeline });

describe('pipelinesReducer MOVE_FUNCTION_REF', () => {
    it('moves a ref between different pipelines', () => {
        let state = initialState;
        state = loadPipeline(state, {
            id: 'p1',
            functions: [
                { id: 'r1', name: 'a' },
                { id: 'r2', name: 'b' },
            ],
        });
        state = loadPipeline(state, {
            id: 'p2',
            functions: [{ id: 'r3', name: 'c' }],
        });

        const next = pipelinesReducer(state, {
            type: 'MOVE_FUNCTION_REF',
            fromPipelineId: 'p1',
            toPipelineId: 'p2',
            refId: 'r2',
            toIdx: 1,
        });

        expect(next.local.p1.functions.map(ref => ref.id)).toEqual(['r1']);
        expect(next.local.p2.functions.map(ref => ref.id)).toEqual(['r3', 'r2']);
    });

    it('keeps same-pipeline no-op semantics for adjacent slot', () => {
        let state = initialState;
        state = loadPipeline(state, {
            id: 'p1',
            functions: [
                { id: 'r1', name: 'a' },
                { id: 'r2', name: 'b' },
            ],
        });

        const next = pipelinesReducer(state, {
            type: 'MOVE_FUNCTION_REF',
            fromPipelineId: 'p1',
            toPipelineId: 'p1',
            refId: 'r1',
            toIdx: 1,
        });

        expect(next).toBe(state);
    });

    it('returns original state when target pipeline is missing', () => {
        let state = initialState;
        state = loadPipeline(state, {
            id: 'p1',
            functions: [{ id: 'r1', name: 'a' }],
        });

        const next = pipelinesReducer(state, {
            type: 'MOVE_FUNCTION_REF',
            fromPipelineId: 'p1',
            toPipelineId: 'missing',
            refId: 'r1',
            toIdx: 0,
        });

        expect(next).toBe(state);
    });
});
