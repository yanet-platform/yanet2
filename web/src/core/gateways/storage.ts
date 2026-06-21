import type { Gateway } from './types';
import { SEED_GATEWAYS, SEED_ACTIVE_ID, BUILTIN_GATEWAY } from './seed';

const STORAGE_KEY = 'yanet_gateways_v1';

interface StoredState {
    gateways: Gateway[];
    activeId: string;
}

/**
 * Ensures the builtin localhost gateway is always the first entry.
 *
 * If the stored list has no builtin entry (e.g. saved before this feature was
 * added), the builtin is prepended so the invariant holds after every load.
 */
const ensureBuiltin = (gateways: Gateway[]): Gateway[] => {
    if (gateways.some((g) => g.builtin === true)) {
        return gateways;
    }
    return [BUILTIN_GATEWAY, ...gateways];
};

/** Load gateways and active id from localStorage, falling back to the seed on any error. */
export const loadFromStorage = (): StoredState => {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) {
            return { gateways: SEED_GATEWAYS, activeId: SEED_ACTIVE_ID };
        }
        const parsed = JSON.parse(raw) as unknown;
        if (
            typeof parsed !== 'object' ||
            parsed === null ||
            !Array.isArray((parsed as StoredState).gateways) ||
            typeof (parsed as StoredState).activeId !== 'string'
        ) {
            return { gateways: SEED_GATEWAYS, activeId: SEED_ACTIVE_ID };
        }
        const state = parsed as StoredState;
        return { gateways: ensureBuiltin(state.gateways), activeId: state.activeId };
    } catch {
        return { gateways: SEED_GATEWAYS, activeId: SEED_ACTIVE_ID };
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
