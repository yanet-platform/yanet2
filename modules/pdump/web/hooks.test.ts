import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const { createRing, deleteRing, listRings } = vi.hoisted(() => ({
    createRing: vi.fn(),
    deleteRing: vi.fn(),
    listRings: vi.fn(),
}));

vi.mock('@yanet/core/api/ring', () => ({
    ring: { createRing, deleteRing, listRings },
}));

import { useRings } from './hooks';

describe('useRings', () => {
    beforeEach(() => {
        createRing.mockReset();
        deleteRing.mockReset();
        listRings.mockReset();
        listRings.mockResolvedValue({ rings: [{ name: 'ring0', capacity: '1048576', publish_batch: 8 }] });
    });

    it('lists the rings the service returns', async () => {
        const { result } = renderHook(() => useRings());
        await waitFor(() => expect(result.current.loading).toBe(false));

        expect(result.current.rings).toEqual([{ name: 'ring0', capacity: '1048576', publish_batch: 8 }]);
    });

    it('creates a ring and refetches the list on success', async () => {
        createRing.mockResolvedValue({});
        listRings
            .mockResolvedValueOnce({ rings: [] })
            .mockResolvedValueOnce({ rings: [{ name: 'ring1', capacity: '8', publish_batch: 8 }] });

        const { result } = renderHook(() => useRings());
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.rings).toEqual([]);

        let created: boolean | undefined;
        await act(async () => {
            created = await result.current.createRing('ring1', 8, undefined);
        });

        expect(created).toBe(true);
        expect(createRing).toHaveBeenCalledWith({ name: 'ring1', capacity: 8, publish_batch: undefined });
        expect(listRings).toHaveBeenCalledTimes(2);
        await waitFor(() => expect(result.current.rings).toEqual([{ name: 'ring1', capacity: '8', publish_batch: 8 }]));
    });

    it('reports a failed create and does not refetch past the initial load', async () => {
        createRing.mockRejectedValue(new Error('name already exists'));

        const { result } = renderHook(() => useRings());
        await waitFor(() => expect(result.current.loading).toBe(false));

        let created: boolean | undefined;
        await act(async () => {
            created = await result.current.createRing('ring0', 8, undefined);
        });

        expect(created).toBe(false);
        expect(listRings).toHaveBeenCalledTimes(1);
    });

    it('deletes a ring and refetches the list on success', async () => {
        deleteRing.mockResolvedValue({});
        listRings
            .mockResolvedValueOnce({ rings: [{ name: 'ring0', capacity: '8', publish_batch: 8 }] })
            .mockResolvedValueOnce({ rings: [] });

        const { result } = renderHook(() => useRings());
        await waitFor(() => expect(result.current.loading).toBe(false));

        let deleted: boolean | undefined;
        await act(async () => {
            deleted = await result.current.deleteRing('ring0');
        });

        expect(deleted).toBe(true);
        expect(deleteRing).toHaveBeenCalledWith({ name: 'ring0' });
        expect(listRings).toHaveBeenCalledTimes(2);
        await waitFor(() => expect(result.current.rings).toEqual([]));
    });

    it('reports a failed delete (e.g. a ring still bound by a config) and does not refetch', async () => {
        deleteRing.mockRejectedValue(new Error('FailedPrecondition: ring is linked by config pdump:main'));

        const { result } = renderHook(() => useRings());
        await waitFor(() => expect(result.current.loading).toBe(false));

        let deleted: boolean | undefined;
        await act(async () => {
            deleted = await result.current.deleteRing('ring0');
        });

        expect(deleted).toBe(false);
        expect(listRings).toHaveBeenCalledTimes(1);
    });
});
