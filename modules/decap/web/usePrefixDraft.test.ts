import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';

const { showConfig, updateConfig } = vi.hoisted(() => ({
    showConfig: vi.fn(),
    updateConfig: vi.fn(),
}));

vi.mock('@yanet/core/api', async () => {
    const actual = await vi.importActual<typeof import('@yanet/core/api')>('@yanet/core/api');
    return {
        ...actual,
        inventoryConfigNames: vi.fn(async () => ['decap0']),
        API: {
            ...actual.API,
            decap: { showConfig, updateConfig },
        },
    };
});

import { usePrefixDraft } from './usePrefixDraft';

describe('usePrefixDraft load', () => {
    beforeEach(() => {
        showConfig.mockReset();
        updateConfig.mockReset();
        updateConfig.mockResolvedValue({});
    });

    it('produces one row per wire element and keeps the prefix string as row identity', async () => {
        showConfig.mockResolvedValue({
            prefixes4: ['10.0.0.0/8'],
            prefixes6: ['2001:db8::/32'],
        });

        const { result } = renderHook(() => usePrefixDraft());
        await waitFor(() => expect(result.current.loading).toBe(false));

        const rows = result.current.draftRows('decap0');
        expect(rows).toHaveLength(2);
        expect(rows.map((r) => r.prefix)).toEqual(['10.0.0.0/8', '2001:db8::/32']);
        expect(rows.map((r) => r.id)).toEqual(['10.0.0.0/8', '2001:db8::/32']);
    });

    it('yields an empty row list when prefixes is omitted from the response', async () => {
        showConfig.mockResolvedValue({});

        const { result } = renderHook(() => usePrefixDraft());
        await waitFor(() => expect(result.current.loading).toBe(false));

        expect(result.current.loadFailed).toBe(false);
        expect(result.current.draftRows('decap0')).toEqual([]);
    });
});

describe('usePrefixDraft commitConfig', () => {
    beforeEach(() => {
        showConfig.mockReset();
        updateConfig.mockReset();
        showConfig.mockResolvedValue({ prefixes4: [], prefixes6: [] });
        updateConfig.mockResolvedValue({});
    });

    it('sends every draft row as a bare string in its family list, dropping none', async () => {
        const { result } = renderHook(() => usePrefixDraft());
        await waitFor(() => expect(result.current.loading).toBe(false));

        act(() => {
            result.current.dispatchDraft({
                type: 'ADD_ROW', configName: 'decap0', row: { id: 'row-1', prefix: '10.0.0.0/8' },
            });
        });
        act(() => {
            result.current.dispatchDraft({
                type: 'ADD_ROW', configName: 'decap0', row: { id: 'row-2', prefix: '' },
            });
        });
        act(() => {
            result.current.dispatchDraft({
                type: 'ADD_ROW', configName: 'decap0', row: { id: 'row-3', prefix: 'not-a-cidr' },
            });
        });

        await act(async () => {
            await result.current.commitConfig('decap0');
        });

        expect(updateConfig).toHaveBeenCalledTimes(1);
        const sent = updateConfig.mock.calls[0][0];
        expect(sent.name).toBe('decap0');
        expect(sent.prefixes4).toEqual(['10.0.0.0/8', '', 'not-a-cidr']);
        expect(sent.prefixes6).toEqual([]);
    });
});
