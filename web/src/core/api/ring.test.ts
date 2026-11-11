import { describe, it, expect, vi, beforeEach } from 'vitest';
import { ring } from './ring';

describe('ring', () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    const stubResponse = (body: unknown): ReturnType<typeof vi.fn> => {
        const fetchMock = vi.fn().mockResolvedValue({
            ok: true,
            status: 200,
            statusText: 'OK',
            json: async () => body,
        });
        vi.stubGlobal('fetch', fetchMock);
        return fetchMock;
    };

    it('sends name, capacity and publish_batch to CreateRing', async () => {
        const fetchMock = stubResponse({});
        await ring.createRing({ name: 'r0', capacity: 1048576, publish_batch: 16 });

        const [url, init] = fetchMock.mock.calls[0];
        expect(url).toBe('/api/objects.ring.controlplane.ringpb.v1.RingService/CreateRing');
        expect(JSON.parse(init.body)).toEqual({ name: 'r0', capacity: 1048576, publish_batch: 16 });
    });

    it('omits publish_batch from CreateRing when not given, letting the service pick its default', async () => {
        const fetchMock = stubResponse({});
        await ring.createRing({ name: 'r0', capacity: 1048576 });

        const [, init] = fetchMock.mock.calls[0];
        expect(JSON.parse(init.body)).toEqual({ name: 'r0', capacity: 1048576 });
    });

    it('calls ListRings with an empty body', async () => {
        const fetchMock = stubResponse({ rings: [{ name: 'r0', capacity: '1048576', publish_batch: 8 }] });
        await expect(ring.listRings()).resolves.toEqual({
            rings: [{ name: 'r0', capacity: '1048576', publish_batch: 8 }],
        });

        const [url, init] = fetchMock.mock.calls[0];
        expect(url).toBe('/api/objects.ring.controlplane.ringpb.v1.RingService/ListRings');
        expect(JSON.parse(init.body)).toEqual({});
    });

    it('sends the name to ShowRing and DeleteRing', async () => {
        const fetchMock = stubResponse({});
        await ring.showRing({ name: 'r0' });
        await ring.deleteRing({ name: 'r0' });

        expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ name: 'r0' });
        expect(fetchMock.mock.calls[0][0]).toBe('/api/objects.ring.controlplane.ringpb.v1.RingService/ShowRing');
        expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ name: 'r0' });
        expect(fetchMock.mock.calls[1][0]).toBe('/api/objects.ring.controlplane.ringpb.v1.RingService/DeleteRing');
    });
});
