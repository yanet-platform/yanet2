import { describe, expect, it } from 'vitest';
import { functionsReducer, initialState } from './reducer';
import type { NetworkFunction } from './types';

const loadFunction = (state: typeof initialState, fn: NetworkFunction) =>
    functionsReducer(state, { type: 'LOAD_ENTITY', id: fn.id, entity: fn });

describe('functionsReducer MOVE_MODULE', () => {
    it('moves a module between different functions', () => {
        let state = initialState;
        state = loadFunction(state, {
            id: 'f1',
            type: 'route',
            chains: [
                {
                    id: 'c1',
                    name: 'chain1',
                    weight: 1,
                    modules: [
                        { id: 'm1', name: 'm1', type: 'route' },
                        { id: 'm2', name: 'm2', type: 'acl' },
                    ],
                },
            ],
        });
        state = loadFunction(state, {
            id: 'f2',
            type: 'route',
            chains: [
                {
                    id: 'c2',
                    name: 'chain2',
                    weight: 1,
                    modules: [{ id: 'm3', name: 'm3', type: 'forward' }],
                },
            ],
        });

        const next = functionsReducer(state, {
            type: 'MOVE_MODULE',
            fromFnId: 'f1',
            toFnId: 'f2',
            fromChainId: 'c1',
            toChainId: 'c2',
            moduleId: 'm2',
            toIdx: 0,
        });

        expect(next.local.f1.chains[0].modules.map(mod => mod.id)).toEqual(['m1']);
        expect(next.local.f2.chains[0].modules.map(mod => mod.id)).toEqual(['m2', 'm3']);
    });

    it('keeps same-chain adjacent slot as no-op', () => {
        let state = initialState;
        state = loadFunction(state, {
            id: 'f1',
            type: 'route',
            chains: [
                {
                    id: 'c1',
                    name: 'chain1',
                    weight: 1,
                    modules: [
                        { id: 'm1', name: 'm1', type: 'route' },
                        { id: 'm2', name: 'm2', type: 'acl' },
                    ],
                },
            ],
        });

        const next = functionsReducer(state, {
            type: 'MOVE_MODULE',
            fromFnId: 'f1',
            toFnId: 'f1',
            fromChainId: 'c1',
            toChainId: 'c1',
            moduleId: 'm1',
            toIdx: 1,
        });

        expect(next.local.f1.chains[0].modules.map(mod => mod.id)).toEqual(['m1', 'm2']);
        expect(next.dirty.has('f1')).toBe(false);
    });

    it('returns original state when target chain is missing', () => {
        let state = initialState;
        state = loadFunction(state, {
            id: 'f1',
            type: 'route',
            chains: [
                {
                    id: 'c1',
                    name: 'chain1',
                    weight: 1,
                    modules: [{ id: 'm1', name: 'm1', type: 'route' }],
                },
            ],
        });

        const next = functionsReducer(state, {
            type: 'MOVE_MODULE',
            fromFnId: 'f1',
            toFnId: 'f1',
            fromChainId: 'c1',
            toChainId: 'missing',
            moduleId: 'm1',
            toIdx: 0,
        });

        expect(next).toBe(state);
    });
});
