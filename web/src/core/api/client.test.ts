import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createService, createStreamingService, ApiError, loadKnownConfigs } from './client';

describe('ApiError', () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    it('carries the numeric status for a 404 response', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: false,
            status: 404,
            statusText: 'Not Found',
            text: async () => '',
        }));
        const service = createService('test.Service');
        await expect(service.call('Method')).rejects.toMatchObject({ status: 404 });
    });

    it('carries the numeric status for a 500 response', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: false,
            status: 500,
            statusText: 'Internal Server Error',
            text: async () => '',
        }));
        const service = createService('test.Service');
        await expect(service.call('Method')).rejects.toMatchObject({ status: 500 });
    });

    it('is an instance of both ApiError and Error', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: false,
            status: 404,
            statusText: 'Not Found',
            text: async () => '',
        }));
        const service = createService('test.Service');
        try {
            await service.call('Method');
            expect.unreachable();
        } catch (err) {
            expect(err).toBeInstanceOf(ApiError);
            expect(err).toBeInstanceOf(Error);
        }
    });
});

describe('finite streaming calls', () => {
    afterEach(() => vi.unstubAllGlobals());

    it('waits for the successful terminal event after fragmented messages', async () => {
        const wire = new TextEncoder().encode('event: message\ndata: {"name":"интерфейс"}\n\nevent: end\ndata: {}\n\n');
        const body = new ReadableStream<Uint8Array>({
            start(controller) {
                for (let offset = 0; offset < wire.length; offset += 3) controller.enqueue(wire.slice(offset, offset + 3));
                controller.close();
            },
        });
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body)));
        const received: unknown[] = [];
        await createStreamingService('test.Service').read('ListStream', {}, (data) => received.push(data));
        expect(received).toEqual([{ name: 'интерфейс' }]);
    });

    it.each([
        ['transport EOF', ''],
        ['server error', 'event: error\ndata: {"code":13,"message":"failed"}\n\n'],
        ['malformed message', 'event: message\ndata: {broken}\n\nevent: end\ndata: {}\n\n'],
    ])('rejects %s after receiving data', async (_name, ending) => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
            `event: message\ndata: {"value":1}\n\n${ending}`,
        )));
        const received: unknown[] = [];
        await expect(createStreamingService('test.Service').read('ListStream', {}, (data) => received.push(data))).rejects.toBeInstanceOf(Error);
        expect(received).toEqual([{ value: 1 }]);
    });

    it('rejects cancellation rather than completing an empty list', async () => {
        const controller = new AbortController();
        const reason = new Error('cancelled');
        controller.abort(reason);
        vi.stubGlobal('fetch', vi.fn().mockRejectedValue(reason));
        await expect(createStreamingService('test.Service').read('ListStream', {}, () => {}, controller.signal)).rejects.toBe(reason);
    });

    it('rejects a callback failure even when a success event follows', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
            'event: message\ndata: {}\n\nevent: end\ndata: {}\n\n',
        )));
        await expect(createStreamingService('test.Service').read('ListStream', {}, () => {
            throw new Error('invalid payload');
        })).rejects.toBeInstanceOf(Error);
    });
});

describe('loadKnownConfigs', () => {
    it('drops a 404 and keeps the surrounding successes in original order', async () => {
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'b') {
                throw new ApiError(404, 'Not Found', '');
            }
            return `loaded-${name}`;
        };
        const results = await loadKnownConfigs(['a', 'b', 'c'], loadOne);
        expect(results).toEqual(['loaded-a', 'loaded-c']);
    });

    it('rejects with the original error object on a 500', async () => {
        const serverError = new ApiError(500, 'Internal Server Error', '');
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'b') {
                throw serverError;
            }
            return `loaded-${name}`;
        };
        await expect(loadKnownConfigs(['a', 'b', 'c'], loadOne)).rejects.toBe(serverError);
    });

    it('rejects on a rejection that is not an ApiError', async () => {
        const networkError = new TypeError('network failure');
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'a') {
                throw networkError;
            }
            return `loaded-${name}`;
        };
        await expect(loadKnownConfigs(['a', 'b'], loadOne)).rejects.toBe(networkError);
    });

    it('calls onDropped with all names when every name 404s', async () => {
        const loadOne = async (): Promise<string> => {
            throw new ApiError(404, 'Not Found', '');
        };
        const onDropped = vi.fn();
        const results = await loadKnownConfigs(['a', 'b', 'c'], loadOne, { onDropped });
        expect(results).toEqual([]);
        expect(onDropped).toHaveBeenCalledTimes(1);
        expect(onDropped).toHaveBeenCalledWith(['a', 'b', 'c']);
    });

    it('calls onDropped with exactly the dropped name when at least one name succeeds', async () => {
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'b') {
                throw new ApiError(404, 'Not Found', '');
            }
            return `loaded-${name}`;
        };
        const onDropped = vi.fn();
        await loadKnownConfigs(['a', 'b'], loadOne, { onDropped });
        expect(onDropped).toHaveBeenCalledTimes(1);
        expect(onDropped).toHaveBeenCalledWith(['b']);
    });

    it('does not call onDropped when names is empty', async () => {
        const loadOne = async (name: string): Promise<string> => `loaded-${name}`;
        const onDropped = vi.fn();
        await loadKnownConfigs([], loadOne, { onDropped });
        expect(onDropped).not.toHaveBeenCalled();
    });

    it('does not call onDropped when every name loads successfully', async () => {
        const loadOne = async (name: string): Promise<string> => `loaded-${name}`;
        const onDropped = vi.fn();
        const results = await loadKnownConfigs(['a', 'b', 'c'], loadOne, { onDropped });
        expect(results).toEqual(['loaded-a', 'loaded-b', 'loaded-c']);
        expect(onDropped).not.toHaveBeenCalled();
    });

    it('does not call onDropped when a non-404 rejection is rethrown', async () => {
        const serverError = new ApiError(500, 'Internal Server Error', '');
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'a') {
                throw serverError;
            }
            throw new ApiError(404, 'Not Found', '');
        };
        const onDropped = vi.fn();
        await expect(loadKnownConfigs(['a', 'b'], loadOne, { onDropped })).rejects.toBe(serverError);
        expect(onDropped).not.toHaveBeenCalled();
    });

    it('preserves original name order even when a later name settles before an earlier one', async () => {
        const delay = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'a') {
                throw new ApiError(404, 'Not Found', '');
            }
            if (name === 'b') {
                await delay(20);
                return `loaded-${name}`;
            }
            await delay(10);
            return `loaded-${name}`;
        };
        const onDropped = vi.fn();
        const results = await loadKnownConfigs(['a', 'b', 'c'], loadOne, { onDropped });
        expect(results).toEqual(['loaded-b', 'loaded-c']);
        expect(onDropped).toHaveBeenCalledWith(['a']);
    });

    it('rejects as soon as one name fails, without waiting for a sibling that never settles', async () => {
        const serverError = new ApiError(500, 'Internal Server Error', '');
        const loadOne = (name: string): Promise<string> => {
            if (name === 'a') {
                return new Promise<never>((_resolve, reject) => reject(serverError));
            }
            return new Promise<string>(() => {});
        };
        await expect(
            Promise.race([
                loadKnownConfigs(['a', 'b'], loadOne),
                new Promise<never>((_resolve, reject) =>
                    setTimeout(() => reject(new Error('loadKnownConfigs did not settle in time')), 100)
                ),
            ])
        ).rejects.toBe(serverError);
    });
});
