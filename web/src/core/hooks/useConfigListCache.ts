import { useCallback, useMemo } from 'react';
import { useGateways } from '../gateways';

/**
 * Lightweight, gateway-scoped snapshot of a page's config tab strip.
 *
 * Holds only the config names and their per-tab counts — never the row
 * payloads — so the cache stays small regardless of how much data a
 * config carries.
 */
export interface ConfigListSnapshot {
    /** Config names in display order. */
    configs: string[];

    /** Per-config item count keyed by config name, for the tab badge. */
    counts: Record<string, number>;
}

const MAX_ENTRIES = 64;

const store = new Map<string, ConfigListSnapshot>();

const composeKey = (gatewayId: string, baseUrl: string, moduleKey: string): string =>
    [gatewayId, baseUrl, moduleKey].join('::');

export interface UseConfigListCacheResult {
    /** Cached snapshot for the active gateway, or null on a cold cache. */
    snapshot: ConfigListSnapshot | null;

    /** Cached config names, or an empty array on a cold cache. */
    configs: string[];

    /** Cached tab counts as a map, empty on a cold cache. */
    counts: Map<string, number>;

    /** Stores the latest snapshot for the active gateway. */
    write: (snapshot: ConfigListSnapshot) => void;
}

const EMPTY_CONFIGS: string[] = [];

/**
 * Gateway-scoped cache of a page's config-name list and tab counts.
 *
 * Lets a page render its ConfigTabStrip instantly on remount instead of
 * blanking behind a full-page loader while the per-config data refetches.
 * The cache is keyed by the active gateway, so switching gateways yields a
 * cold cache (and a one-time loader) rather than another gateway's tabs.
 */
export const useConfigListCache = (moduleKey: string): UseConfigListCacheResult => {
    const { activeGateway } = useGateways();
    const key = composeKey(activeGateway?.id ?? '', activeGateway?.baseUrl ?? '', moduleKey);

    const snapshot = store.get(key) ?? null;

    const counts = useMemo(() => {
        const map = new Map<string, number>();
        if (snapshot) {
            for (const [name, count] of Object.entries(snapshot.counts)) {
                map.set(name, count);
            }
        }
        return map;
    }, [snapshot]);

    const write = useCallback((next: ConfigListSnapshot): void => {
        if (!store.has(key) && store.size >= MAX_ENTRIES) {
            const oldest = store.keys().next().value;
            if (oldest !== undefined) {
                store.delete(oldest);
            }
        }
        store.set(key, next);
    }, [key]);

    return { snapshot, configs: snapshot?.configs ?? EMPTY_CONFIGS, counts, write };
};
