import { describe, it, expect } from 'vitest';
import { createDraftReducer } from './draftReducer';

interface Row {
    id: string;
    value: string;
}

const { reducer, initialState } = createDraftReducer<Row>({
    getId: (r) => r.id,
    equals: (a, b) => a.value === b.value,
});

describe('REFRESH_CONFIG', () => {
    it('updates only the named config, leaving other tabs and dirty state intact', () => {
        const loaded = reducer(initialState, {
            type: 'LOAD_ALL_CONFIGS',
            configs: [
                { name: 'route0', rows: [{ id: 'r0', value: 'a' }] },
                { name: 'route1', rows: [{ id: 'r1', value: 'b' }] },
            ],
        });
        const edited = reducer(loaded, {
            type: 'UPDATE_ROW',
            configName: 'route1',
            id: 'r1',
            patch: { value: 'edited' },
        });
        expect(edited.dirty.has('route1')).toBe(true);

        const refreshed = reducer(edited, {
            type: 'REFRESH_CONFIG',
            configName: 'route0',
            rows: [{ id: 'r0-new', value: 'a-refreshed' }],
        });

        expect(refreshed.serverConfigs).toEqual(['route0', 'route1']);
        expect(refreshed.draft['route1']).toEqual([{ id: 'r1', value: 'edited' }]);
        expect(refreshed.dirty.has('route1')).toBe(true);
        expect(refreshed.dirty.has('route0')).toBe(false);
        expect(refreshed.draft['route0']).toEqual([{ id: 'r0-new', value: 'a-refreshed' }]);
    });

    it('keeps a dirty draft untouched while updating its server snapshot', () => {
        const loaded = reducer(initialState, {
            type: 'LOAD_ALL_CONFIGS',
            configs: [{ name: 'route0', rows: [{ id: 'r0', value: 'a' }] }],
        });
        const edited = reducer(loaded, {
            type: 'UPDATE_ROW',
            configName: 'route0',
            id: 'r0',
            patch: { value: 'edited-by-user' },
        });
        expect(edited.dirty.has('route0')).toBe(true);

        const refreshed = reducer(edited, {
            type: 'REFRESH_CONFIG',
            configName: 'route0',
            rows: [{ id: 'r0', value: 'changed-on-server' }],
        });

        expect(refreshed.draft['route0']).toEqual([{ id: 'r0', value: 'edited-by-user' }]);
        expect(refreshed.server['route0']).toEqual([{ id: 'r0', value: 'changed-on-server' }]);
        expect(refreshed.dirty.has('route0')).toBe(true);
    });
});
