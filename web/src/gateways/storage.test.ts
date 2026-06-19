import { describe, it, expect, beforeEach, vi } from 'vitest';
import { loadFromStorage, saveToStorage } from './storage';
import { SEED_GATEWAYS, SEED_ACTIVE_ID, BUILTIN_GATEWAY } from './seed';

const STORAGE_KEY = 'yanet_gateways_v1';

const makeMockStorage = (): Storage => {
    const store: Record<string, string> = {};
    return {
        getItem: (key: string) => store[key] ?? null,
        setItem: (key: string, value: string) => { store[key] = value; },
        removeItem: (key: string) => { delete store[key]; },
        clear: () => { Object.keys(store).forEach((k) => delete store[k]); },
        get length() { return Object.keys(store).length; },
        key: (idx: number) => Object.keys(store)[idx] ?? null,
    };
};

describe('loadFromStorage', () => {
    let mockStorage: Storage;

    beforeEach(() => {
        mockStorage = makeMockStorage();
        vi.stubGlobal('localStorage', mockStorage);
    });

    it('returns seed data when localStorage is empty', () => {
        const result = loadFromStorage();
        expect(result.gateways).toEqual(SEED_GATEWAYS);
        expect(result.activeId).toBe(SEED_ACTIVE_ID);
    });

    it('returns seed data when localStorage contains invalid JSON', () => {
        mockStorage.setItem(STORAGE_KEY, 'not-json{{{');
        const result = loadFromStorage();
        expect(result.gateways).toEqual(SEED_GATEWAYS);
        expect(result.activeId).toBe(SEED_ACTIVE_ID);
    });

    it('returns seed data when stored state has wrong shape', () => {
        mockStorage.setItem(STORAGE_KEY, JSON.stringify({ wrong: true }));
        const result = loadFromStorage();
        expect(result.gateways).toEqual(SEED_GATEWAYS);
        expect(result.activeId).toBe(SEED_ACTIVE_ID);
    });

    it('returns seed data when gateways field is not an array', () => {
        mockStorage.setItem(STORAGE_KEY, JSON.stringify({ gateways: 'nope', activeId: 'x' }));
        const result = loadFromStorage();
        expect(result.gateways).toEqual(SEED_GATEWAYS);
    });

    it('returns persisted data when storage is valid', () => {
        const extra = { id: 'gw-01', host: 'gateway-01', numa: 0, addr: '10.0.0.10:8080', baseUrl: 'http://10.0.0.10:8080', status: 'online' as const };
        const stored = { gateways: [...SEED_GATEWAYS, extra], activeId: 'gw-01' };
        mockStorage.setItem(STORAGE_KEY, JSON.stringify(stored));
        const result = loadFromStorage();
        expect(result.gateways).toHaveLength(2);
        expect(result.activeId).toBe('gw-01');
    });

    it('re-injects the builtin gateway when stored list lacks one', () => {
        const withoutBuiltin = SEED_GATEWAYS.filter((g) => !g.builtin);
        mockStorage.setItem(STORAGE_KEY, JSON.stringify({ gateways: withoutBuiltin, activeId: 'fra01-n0' }));
        const result = loadFromStorage();
        expect(result.gateways[0]).toEqual(BUILTIN_GATEWAY);
        expect(result.gateways.some((g) => g.builtin === true)).toBe(true);
    });
});

describe('saveToStorage + loadFromStorage round-trip', () => {
    let mockStorage: Storage;

    beforeEach(() => {
        mockStorage = makeMockStorage();
        vi.stubGlobal('localStorage', mockStorage);
    });

    it('persists and reloads the gateway list', () => {
        saveToStorage(SEED_GATEWAYS, 'fra01-n1');
        const result = loadFromStorage();
        expect(result.gateways).toEqual(SEED_GATEWAYS);
        expect(result.activeId).toBe('fra01-n1');
    });

    it('builtin gateway survives a save/load round-trip with its flag intact', () => {
        saveToStorage(SEED_GATEWAYS, SEED_ACTIVE_ID);
        const result = loadFromStorage();
        const builtin = result.gateways.find((g) => g.builtin === true);
        expect(builtin).toBeDefined();
        expect(builtin).toEqual(BUILTIN_GATEWAY);
    });
});
