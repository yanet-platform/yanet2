import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { pdumpApi, PDUMP_RATE_PPS_MAX } from './pdump';

describe('pdump capture rate requests', () => {
    const fetchMock = vi.fn();

    beforeEach(() => {
        fetchMock.mockReset().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            json: async () => ({}),
        });
        vi.stubGlobal('fetch', fetchMock);
    });

    afterEach(() => vi.unstubAllGlobals());

    it.each([undefined, 0, PDUMP_RATE_PPS_MAX])('preserves rate presence and the exact accepted value %s', async rate => {
        await pdumpApi.setConfig('capture', { rate_pps: rate });
        const body = JSON.parse(fetchMock.mock.calls[0][1].body);
        expect(body).toEqual({ name: 'capture', config: rate === undefined ? {} : { rate_pps: rate } });
    });

    it.each([PDUMP_RATE_PPS_MAX + 1, -1, 0.5, Infinity, NaN])('rejects invalid rate %s before a network request', async rate => {
        await expect(pdumpApi.setConfig('capture', { rate_pps: rate })).rejects.toThrow('Rate must be an integer');
        expect(fetchMock).not.toHaveBeenCalled();
    });
});
