import { afterEach, describe, expect, it, vi } from 'vitest';
import { neighbours } from './neighbours';

describe('neighbour listing', () => {
    afterEach(() => vi.unstubAllGlobals());

    it.each([undefined, 'static'])('collects every chunk for table %s', async (table) => {
        const entries = Array.from({ length: 1001 }, (_, index) => ({
            next_hop: `2001:db8::${(index + 1).toString(16)}`,
            device: 'logical0',
            source: index < 500 ? 'static' : 'remote',
        }));
        const chunks = [entries.slice(0, 500), entries.slice(500)];
        const fetchMock = vi.fn().mockResolvedValue(new Response(
            chunks.map((chunk) => `event: message\ndata: ${JSON.stringify({ neighbours: chunk })}\n\n`).join('') +
            'event: end\ndata: {}\n\n',
        ));
        vi.stubGlobal('fetch', fetchMock);
        await expect(neighbours.list(table)).resolves.toEqual({ neighbours: entries });
        expect(fetchMock).toHaveBeenCalledWith('/api/operators.route.operatorpb.v1.NeighbourService/ListStream', expect.objectContaining({
            body: JSON.stringify({ table: table ?? '' }),
        }));
    });

    it('returns an empty list only after successful completion', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
            'event: message\ndata: {}\n\nevent: end\ndata: {}\n\n',
        )));
        await expect(neighbours.list()).resolves.toEqual({ neighbours: [] });
    });

    it('does not return a partial list when the transport closes early', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
            'event: message\ndata: {"neighbours":[{"next_hop":"192.0.2.1"}]}\n\n',
        )));
        await expect(neighbours.list()).rejects.toThrow('without successful completion');
    });
});
