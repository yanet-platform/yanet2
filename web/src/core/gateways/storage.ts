import type { Gateway } from './types';
import {
    SEED_GATEWAYS,
    SEED_ACTIVE_ID,
    BUILTIN_GATEWAY,
    builtinFromConfig,
    seedGatewaysFromConfig,
    extraGatewaysFromConfig,
} from './seed';
import type { RuntimeConfig } from './runtimeConfig';

const STORAGE_KEY = 'yanet_gateways_v1';

interface StoredState {
    gateways: Gateway[];
    activeId: string;
}

/**
 * Merges stored gateways with the runtime config.
 *
 * When a config is provided:
 * - The stored builtin is replaced with the config-derived one so
 *   `defaultBackendUrl` changes take effect for returning users.
 * - Config-defined extra gateways are appended, deduped by `addr` so
 *   users who already added the same backend don't get a duplicate.
 *
 * Without a config, the stored builtin is kept as-is (or the default
 * same-origin one is prepended if missing).
 */
const mergeWithConfig = (gateways: Gateway[], config?: RuntimeConfig): Gateway[] => {
    const builtin = config ? builtinFromConfig(config) : BUILTIN_GATEWAY;

    if (!config) {
        if (gateways.some((g) => g.builtin === true)) {
            return gateways;
        }
        return [builtin, ...gateways];
    }

    const rest = gateways.filter((g) => !g.builtin);
    const existingAddrs = new Set(rest.map((g) => g.addr));
    const extras = extraGatewaysFromConfig(config).filter((g) => !existingAddrs.has(g.addr));

    return [builtin, ...rest, ...extras];
};

/** Load gateways and active id from localStorage, falling back to the seed on any error. */
export const loadFromStorage = (config?: RuntimeConfig): StoredState => {
    const seed = config ? seedGatewaysFromConfig(config) : SEED_GATEWAYS;
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) {
            return { gateways: seed, activeId: SEED_ACTIVE_ID };
        }
        const parsed = JSON.parse(raw) as unknown;
        if (
            typeof parsed !== 'object' ||
            parsed === null ||
            !Array.isArray((parsed as StoredState).gateways) ||
            typeof (parsed as StoredState).activeId !== 'string'
        ) {
            return { gateways: seed, activeId: SEED_ACTIVE_ID };
        }
        const state = parsed as StoredState;
        return { gateways: mergeWithConfig(state.gateways, config), activeId: state.activeId };
    } catch {
        return { gateways: seed, activeId: SEED_ACTIVE_ID };
    }
};

/** Persist gateways and active id to localStorage. */
export const saveToStorage = (gateways: Gateway[], activeId: string): void => {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify({ gateways, activeId }));
    } catch {
        // Storage quota exceeded or private browsing — ignore.
    }
};
