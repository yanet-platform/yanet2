import { useCallback, useEffect, useState } from 'react';
import { API } from '../../api';
import { toaster } from '../../utils';
import type { Route } from '../../api/routes';

export interface UseRouteDataResult {
    configs: string[];
    configRoutes: Map<string, Route[]>;
    selectedRoutes: Map<string, Set<string>>;
    loading: boolean;
    activeConfigTab: string;
    setConfigs: React.Dispatch<React.SetStateAction<string[]>>;
    setConfigRoutes: React.Dispatch<React.SetStateAction<Map<string, Route[]>>>;
    setSelectedRoutes: React.Dispatch<React.SetStateAction<Map<string, Set<string>>>>;
    setActiveConfigTab: React.Dispatch<React.SetStateAction<string>>;
    handleSelectionChange: (configName: string, selectedIds: string[]) => void;
    handleConfigTabChange: (config: string) => void;
    reloadRoutes: (configsList: string[]) => Promise<Map<string, Route[]>>;
}

export const useRouteData = (): UseRouteDataResult => {
    const [configs, setConfigs] = useState<string[]>([]);
    const [configRoutes, setConfigRoutes] = useState<Map<string, Route[]>>(new Map());
    const [selectedRoutes, setSelectedRoutes] = useState<Map<string, Set<string>>>(new Map());
    const [loading, setLoading] = useState<boolean>(true);
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');

    // Data loading
    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                const configsResponse = await API.route.listConfigs();
                const configsList = configsResponse.configs || [];

                const routesMap = new Map<string, Route[]>();

                await Promise.all(
                    configsList.map(async (configName) => {
                        try {
                            const routesResponse = await API.route.showRoutes({
                                target: {
                                    configName,
                                },
                            });
                            const routes = routesResponse.routes || [];
                            routesMap.set(configName, routes);
                        } catch (err) {
                            if (!isMounted) return;
                            toaster.error(`route-fetch-error-${configName}`, `Failed to load routes for ${configName}`, err);
                        }
                    })
                );

                if (!isMounted) return;

                setConfigs(configsList);
                setConfigRoutes(routesMap);
                if (configsList.length > 0) {
                    setActiveConfigTab(configsList[0]);
                }
            } catch (err) {
                if (!isMounted) return;
                toaster.error('route-error', 'Failed to fetch route data', err);
            } finally {
                if (isMounted) {
                    setLoading(false);
                }
            }
        };

        loadData();

        return () => {
            isMounted = false;
        };
    }, []);

    const handleSelectionChange = useCallback((configName: string, selectedIds: string[]): void => {
        setSelectedRoutes((prev) => {
            const newSelected = new Map(prev);
            newSelected.set(configName, new Set(selectedIds));
            return newSelected;
        });
    }, []);

    const handleConfigTabChange = useCallback((config: string): void => {
        setActiveConfigTab(config);
    }, []);

    const reloadRoutes = useCallback(async (configsList: string[]): Promise<Map<string, Route[]>> => {
        const routesMap = new Map<string, Route[]>();

        for (const configName of configsList) {
            try {
                const routesResponse = await API.route.showRoutes({
                    target: {
                        configName,
                    },
                });
                routesMap.set(configName, routesResponse.routes || []);
            } catch (err) {
                toaster.error(`reload-route-error-${configName}`, `Failed to reload routes for ${configName}`, err);
            }
        }

        return routesMap;
    }, []);

    return {
        configs,
        configRoutes,
        selectedRoutes,
        loading,
        activeConfigTab,
        setConfigs,
        setConfigRoutes,
        setSelectedRoutes,
        setActiveConfigTab,
        handleSelectionChange,
        handleConfigTabChange,
        reloadRoutes,
    };
};
