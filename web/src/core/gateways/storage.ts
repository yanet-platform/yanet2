import type { Gateway } from './types';
import {
    SEED_GATEWAYS,
    SEED_ACTIVE_ID,
    BUILTIN_GATEWAY,
    builtinFromConfig,
    seedGatewaysFromConfig,
} from './seed';
import type { RuntimeConfig } from './runtimeConfig';

const STORAGE_KEY = 'yanet_gateways_v1';

interface StoredState {
    gateways: Gateway[];
    activeId: string;
}

/**
 * Ensures the builtin gateway is always the first entry.
 *
 * If the stored list has no builtin entry (e.g. saved before this feature was
 * added), the builtin is prepended so the invariant holds after every load.
 *
 * When a runtime config is provided, any stored builtin is replaced with the
 * config-derived one so `defaultBackendUrl` changes take effect even for
 * users who previously saved a same-origin builtin.
 */
const ensureBuiltin = (gateways: Gateway[], config?: RuntimeConfig): Gateway[] => {
    const builtin = config ? builtinFromConfig(config) : BUILTIN_GATEWAY;
    if (config) {
        const rest = gateways.filter((g) => !g.builtin);
        return [builtin, ...rest];
    }
    if (gateways.some((g) => g.builtin === true)) {
        return gateways;
    }
    return [builtin, ...gateways];
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
        return { gateways: ensureBuiltin(state.gateways, config), activeId: state.activeId };
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
