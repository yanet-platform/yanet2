import { useCallback, useEffect, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { Route } from '../../../api/routes';

export interface UseRIBResult {
    configs: string[];
    configRoutes: Map<string, Route[]>;
    selectedIds: Map<string, Set<string>>;
    loading: boolean;
    refreshing: boolean;
    reload: () => Promise<void>;
    addLocalConfig: (name: string) => void;
    setSelected: (configName: string, ids: Set<string>) => void;
}

const sortConfigs = (a: string, b: string): number =>
    a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' });

/** Loads route configs and their routes from the operator backend. */
export const useRIB = (): UseRIBResult => {
    const [configs, setConfigs] = useState<string[]>([]);
    const [configRoutes, setConfigRoutes] = useState<Map<string, Route[]>>(new Map());
    const [selectedIds, setSelectedIds] = useState<Map<string, Set<string>>>(new Map());
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);

    const loadAll = useCallback(async (opts?: { initial?: boolean; isCancelled?: () => boolean }): Promise<void> => {
        const isInitial = opts?.initial !== false;
        if (isInitial) {
            setLoading(true);
        } else {
            setRefreshing(true);
        }
        try {
            const configsResponse = await API.routeOperator.listConfigs();
            const configsList = configsResponse.configs || [];

            const routesMap = new Map<string, Route[]>();
            await Promise.all(
                configsList.map(async (name, idx) => {
                    try {
                        const res = await API.route.showRoutes({ name });
                        routesMap.set(name, res.routes || []);
                    } catch (err) {
                        toaster.error(`route-fetch-error-${idx}`, `Failed to load routes for ${name}`, err);
                    }
                })
            );

            if (opts?.isCancelled?.()) return;
            setConfigs([...configsList].sort(sortConfigs));
            setConfigRoutes(routesMap);
        } catch (err) {
            if (opts?.isCancelled?.()) return;
            toaster.error('rib-configs-error', 'Failed to fetch RIB configs', err);
        } finally {
            if (!opts?.isCancelled?.()) {
                if (isInitial) {
                    setLoading(false);
                } else {
                    setRefreshing(false);
                }
            }
        }
    }, []);

    useEffect(() => {
        let cancelled = false;
        loadAll({ initial: true, isCancelled: () => cancelled });
        return () => { cancelled = true; };
    }, [loadAll]);

    const reload = useCallback(async (): Promise<void> => {
        await loadAll({ initial: false });
    }, [loadAll]);

    const addLocalConfig = useCallback((name: string): void => {
        setConfigs((prev) => (prev.includes(name) ? prev : [...prev, name].sort(sortConfigs)));
        setConfigRoutes((prev) => {
            const next = new Map(prev);
            if (!next.has(name)) next.set(name, []);
            return next;
        });
    }, []);

    const setSelected = useCallback((configName: string, ids: Set<string>): void => {
        setSelectedIds((prev) => {
            const next = new Map(prev);
            next.set(configName, ids);
            return next;
        });
    }, []);

    return { configs, configRoutes, selectedIds, loading, refreshing, reload, addLocalConfig, setSelected };
};
