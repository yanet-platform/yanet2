import { describe, it, expect, vi, beforeEach } from 'vitest';
import { builtinFromConfig, seedGatewaysFromConfig, loadRuntimeConfig } from './runtimeConfig';
import type { RuntimeConfig } from './runtimeConfig';

const EMPTY_CONFIG: RuntimeConfig = { defaultBackendUrl: '', gateways: [] };

describe('builtinFromConfig', () => {
    it('returns a same-origin builtin when defaultBackendUrl is empty', () => {
        const gw = builtinFromConfig(EMPTY_CONFIG);
        expect(gw.id).toBe('localhost');
        expect(gw.baseUrl).toBe('');
        expect(gw.addr).toBe('same-origin');
        expect(gw.builtin).toBe(true);
    });

    it('returns a configured builtin when defaultBackendUrl is set', () => {
        const gw = builtinFromConfig({ ...EMPTY_CONFIG, defaultBackendUrl: 'http://10.0.0.5:8081' });
        expect(gw.id).toBe('localhost');
        expect(gw.baseUrl).toBe('http://10.0.0.5:8081');
        expect(gw.addr).toBe('http://10.0.0.5:8081');
        expect(gw.host).toBe('10.0.0.5');
        expect(gw.builtin).toBe(true);
    });

    it('falls back to "default" host for malformed URLs', () => {
        const gw = builtinFromConfig({ ...EMPTY_CONFIG, defaultBackendUrl: 'not-a-url' });
        expect(gw.host).toBe('default');
        expect(gw.baseUrl).toBe('not-a-url');
    });
});

describe('seedGatewaysFromConfig', () => {
    it('returns only the builtin gateway when config has no extra gateways', () => {
        const seed = seedGatewaysFromConfig(EMPTY_CONFIG);
        expect(seed).toHaveLength(1);
        expect(seed[0].builtin).toBe(true);
    });

    it('appends extra gateways from config', () => {
        const config: RuntimeConfig = {
            defaultBackendUrl: 'http://gw1:8081',
            gateways: [
                { host: 'gw2', numa: 0, addr: 'gw2:8081' },
                { host: 'gw3', numa: 1, addr: 'https://gw3:8081' },
            ],
        };
        const seed = seedGatewaysFromConfig(config);
        expect(seed).toHaveLength(3);
        expect(seed[0].builtin).toBe(true);
        expect(seed[0].baseUrl).toBe('http://gw1:8081');
        expect(seed[1].host).toBe('gw2');
        expect(seed[1].baseUrl).toBe('http://gw2:8081');
        expect(seed[2].host).toBe('gw3');
        expect(seed[2].baseUrl).toBe('https://gw3:8081');
    });

    it('derives baseUrl from addr for extra gateways', () => {
        const config: RuntimeConfig = {
            defaultBackendUrl: '',
            gateways: [{ host: 'gw', numa: 0, addr: '10.0.0.10:8080' }],
        };
        const seed = seedGatewaysFromConfig(config);
        expect(seed[1].baseUrl).toBe('http://10.0.0.10:8080');
    });
});

describe('loadRuntimeConfig', () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    it('returns empty config when fetch returns 404', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404 }));
        const config = await loadRuntimeConfig();
        expect(config).toEqual(EMPTY_CONFIG);
    });

    it('returns parsed config on success', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: true,
            json: async () => ({
                defaultBackendUrl: 'http://gw1:8081',
                gateways: [{ host: 'gw2', numa: 0, addr: 'gw2:8081' }],
            }),
        }));
        const config = await loadRuntimeConfig();
        expect(config.defaultBackendUrl).toBe('http://gw1:8081');
        expect(config.gateways).toHaveLength(1);
        expect(config.gateways[0].host).toBe('gw2');
    });

    it('returns empty config on fetch error', async () => {
        vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network')));
        const config = await loadRuntimeConfig();
        expect(config).toEqual(EMPTY_CONFIG);
    });

    it('returns empty config when response is not valid JSON', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: true,
            json: async () => { throw new Error('parse error'); },
        }));
        const config = await loadRuntimeConfig();
        expect(config).toEqual(EMPTY_CONFIG);
    });

    it('filters out gateway entries with empty addr', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: true,
            json: async () => ({
                gateways: [
                    { host: 'gw1', numa: 0, addr: 'gw1:8081' },
                    { host: 'gw2', numa: 0, addr: '' },
                ],
            }),
        }));
        const config = await loadRuntimeConfig();
        expect(config.gateways).toHaveLength(1);
        expect(config.gateways[0].host).toBe('gw1');
    });

    it('defaults numa to 0 when missing', async () => {
        vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
            ok: true,
            json: async () => ({
                gateways: [{ host: 'gw', addr: 'gw:8081' }],
            }),
        }));
        const config = await loadRuntimeConfig();
        expect(config.gateways[0].numa).toBe(0);
    });
});
