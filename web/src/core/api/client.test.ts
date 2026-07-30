import { describe, it, expect, vi, beforeEach } from 'vitest';
import { createService, ApiError, loadKnownConfigs } from './client';

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

    it('calls onAllDropped with the name count when every name 404s', async () => {
        const loadOne = async (): Promise<string> => {
            throw new ApiError(404, 'Not Found', '');
        };
        const onAllDropped = vi.fn();
        const results = await loadKnownConfigs(['a', 'b', 'c'], loadOne, { onAllDropped });
        expect(results).toEqual([]);
        expect(onAllDropped).toHaveBeenCalledTimes(1);
        expect(onAllDropped).toHaveBeenCalledWith(3);
    });

    it('does not call onAllDropped when at least one name succeeds', async () => {
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'b') {
                throw new ApiError(404, 'Not Found', '');
            }
            return `loaded-${name}`;
        };
        const onAllDropped = vi.fn();
        await loadKnownConfigs(['a', 'b'], loadOne, { onAllDropped });
        expect(onAllDropped).not.toHaveBeenCalled();
    });

    it('does not call onAllDropped when names is empty', async () => {
        const loadOne = async (name: string): Promise<string> => `loaded-${name}`;
        const onAllDropped = vi.fn();
        await loadKnownConfigs([], loadOne, { onAllDropped });
        expect(onAllDropped).not.toHaveBeenCalled();
    });

    it('does not call onAllDropped when a non-404 rejection is rethrown', async () => {
        const serverError = new ApiError(500, 'Internal Server Error', '');
        const loadOne = async (name: string): Promise<string> => {
            if (name === 'a') {
                throw serverError;
            }
            throw new ApiError(404, 'Not Found', '');
        };
        const onAllDropped = vi.fn();
        await expect(loadKnownConfigs(['a', 'b'], loadOne, { onAllDropped })).rejects.toBe(serverError);
        expect(onAllDropped).not.toHaveBeenCalled();
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
