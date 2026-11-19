import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const { createRing, deleteRing, listRings } = vi.hoisted(() => ({
    createRing: vi.fn(),
    deleteRing: vi.fn(),
    listRings: vi.fn(),
}));

vi.mock('@yanet/core/api/ring', () => ({
    ring: { createRing, deleteRing, listRings },
}));

import { pdumpApi } from '@yanet/core/api/pdump';
import { usePdumpCapture, useRings } from './hooks';

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

describe('capture ring loss', () => {
    afterEach(() => vi.restoreAllMocks());

    it('keeps legacy packets and publishes the accumulated loss alongside received packets', async () => {
        const stream = vi.spyOn(pdumpApi, 'readDump').mockImplementation(() => {});
        const { result, unmount } = renderHook(() => usePdumpCapture(false));
        act(() => result.current.startCapture('demo'));
        const { onMessage } = stream.mock.calls[0]![1];
        act(() => {
            onMessage({ data: 'AQI=' });
            onMessage({ data: 'AwQ=', lost_before: 3 });
            onMessage({ data: 'BQY=', lost_before: 4 });
        });
        await waitFor(() => expect(result.current.lossByConfig.demo).toBe(7));
        expect(result.current.packetsByConfig.demo?.map(p => Array.from(p.parsed.raw))).toEqual([[1, 2], [3, 4], [5, 6]]);
        unmount();
    });

    it('counts losses while paused, isolates streams and keeps the final count until restart', () => {
        const stream = vi.spyOn(pdumpApi, 'readDump').mockImplementation(() => {});
        const { result, unmount } = renderHook(() => usePdumpCapture(true));
        act(() => result.current.startCapture('a'));
        const first = stream.mock.calls[0]![1];
        act(() => {
            first.onMessage({ lost_before: 7 });
            result.current.startCapture('b');
        });
        const second = stream.mock.calls[1]![1];
        act(() => {
            first.onMessage({ lost_before: 20 });
            first.onEnd?.();
            second.onMessage({ lost_before: 2 });
            result.current.clearPackets('a');
            second.onMessage({ lost_before: 3 });
        });
        expect(result.current.liveConfig).toBe('b');
        act(() => result.current.stopCapture());
        expect(result.current.lossByConfig).toEqual({ a: 7, b: 5 });
        act(() => result.current.startCapture('a'));
        expect(result.current.lossByConfig).toEqual({ a: 0, b: 5 });
        unmount();
    });

    it('restarts packet numbering after clearing a stopped capture', async () => {
        const stream = vi.spyOn(pdumpApi, 'readDump').mockImplementation(() => {});
        const { result, unmount } = renderHook(() => usePdumpCapture(false));
        act(() => result.current.startCapture('demo'));
        act(() => stream.mock.calls[0]![1].onMessage({ data: 'AQI=' }));
        await waitFor(() => expect(result.current.packetsByConfig.demo).toHaveLength(1));
        act(() => {
            result.current.stopCapture();
            result.current.clearPackets('demo');
            result.current.startCapture('demo');
        });
        act(() => stream.mock.calls[1]![1].onMessage({ data: 'AwQ=' }));
        await waitFor(() => expect(result.current.packetsByConfig.demo).toHaveLength(1));
        expect(result.current.packetsByConfig.demo?.[0]?.id).toBe(0);
        unmount();
    });
});
