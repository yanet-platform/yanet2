import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { API } from '@yanet/core/api';
import { toaster } from '@yanet/core/utils';
import type { Neighbour } from '@yanet/core/api/neighbours';
import { MERGED_TAB } from './types';
import { useNeighbours } from './useNeighbours';

vi.mock('@yanet/core/api', () => ({
    API: { neighbours: { listTables: vi.fn(), list: vi.fn() } },
}));
vi.mock('@yanet/core/utils', () => ({ toaster: { error: vi.fn() } }));

afterEach(() => {
    cleanup();
    vi.resetAllMocks();
});

describe('neighbour table prefetch', () => {
    it('bounds concurrent reads and continues through all tables after a read failure', async () => {
        const tables = ['a', 'b', 'c', 'd', 'e', 'f'];
        vi.mocked(API.neighbours.listTables).mockResolvedValue({
            tables: tables.map((name) => ({ name })),
        });
        const pending: { finish: () => void }[] = [];
        let active = 0;
        let peak = 0;
        vi.mocked(API.neighbours.list).mockImplementation((table) => {
            active++;
            peak = Math.max(peak, active);
            return new Promise<{ neighbours: Neighbour[] }>((resolve, reject) => {
                pending.push({ finish: () => {
                    active--;
                    if (table === 'b') {
                        reject(new Error('read budget exceeded'));
                    } else {
                        resolve({ neighbours: [{ next_hop: '192.0.2.1', device: table ?? 'merged' }] });
                    }
                } });
            });
        });
        const { result } = renderHook(() => useNeighbours(MERGED_TAB, true));
        for (let completed = 0; completed < tables.length + 1;) {
            await waitFor(() => expect(pending.length).toBeGreaterThan(0));
            expect(active).toBeLessThanOrEqual(2);
            await act(async () => {
                const batch = pending.splice(0);
                completed += batch.length;
                for (const request of batch) request.finish();
            });
        }
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(peak).toBe(2);
        expect(API.neighbours.list).toHaveBeenCalledTimes(tables.length + 1);
        expect(API.neighbours.list).toHaveBeenCalledWith(undefined);
        for (const table of tables) expect(API.neighbours.list).toHaveBeenCalledWith(table);
        expect([...result.current.cache.keys()]).toEqual([MERGED_TAB, 'a', 'c', 'd', 'e', 'f']);
        for (const entries of result.current.cache.values()) expect(entries).toHaveLength(1);
        expect(toaster.error).toHaveBeenCalledOnce();
    });
});
