import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const { showFIB, updateFIB, listConfigs } = vi.hoisted(() => ({
    showFIB: vi.fn(),
    updateFIB: vi.fn(),
    listConfigs: vi.fn(),
}));

vi.mock('@yanet/core/api', async () => {
    const actual = await vi.importActual<typeof import('@yanet/core/api')>('@yanet/core/api');
    return {
        ...actual,
        inventoryConfigNames: vi.fn(async () => []),
        API: {
            ...actual.API,
            route: { listConfigs, showFIB, updateFIB },
        },
    };
});

import { useFIBDraft } from './useFIBDraft';

const fibResp = (device: string) => ({
    entries: [{
        range: { start: '10.0.0.0', end: '10.0.0.255' },
        nexthops: [{ dst_mac: 'aa:bb:cc:dd:ee:ff', src_mac: '11:22:33:44:55:66', device, counter: '' }],
    }],
});

describe('useFIBDraft commitConfig', () => {
    beforeEach(() => {
        showFIB.mockReset();
        updateFIB.mockReset();
        listConfigs.mockReset();
        listConfigs.mockResolvedValue({ configs: ['route0', 'route1'] });
        showFIB.mockImplementation(async ({ name }: { name: string }) => fibResp(name === 'route0' ? 'eth0' : 'eth1'));
        updateFIB.mockResolvedValue({});
    });

    it('leaves other configs\' tabs and dirty state intact after committing one', async () => {
        const { result } = renderHook(() => useFIBDraft());

        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.draftConfigs).toEqual(['route0', 'route1']);

        act(() => {
            result.current.dispatchDraft({
                type: 'UPDATE_ROW',
                configName: 'route1',
                id: result.current.draftRows('route1')[0].id,
                patch: { device: 'eth9' },
            });
        });
        expect(result.current.isDirty('route1')).toBe(true);

        await act(async () => {
            await result.current.commitConfig('route0');
        });

        expect(result.current.draftConfigs).toEqual(['route0', 'route1']);
        expect(result.current.isDirty('route1')).toBe(true);
        expect(result.current.draftRows('route1')[0].device).toBe('eth9');
    });
});
