import { useCallback, useEffect, useState } from 'react';
import { API } from '../../api';
import { toaster } from '../../utils';
import type { InstanceConfigs, Route } from '../../api/routes';

export interface UseRouteDataResult {
    instanceConfigs: InstanceConfigs[];
    instanceRoutes: Map<number, Map<string, Route[]>>;
    selectedRoutes: Map<number, Map<string, Set<string>>>;
    loading: boolean;
    activeConfigTab: Map<number, string>;
    setInstanceConfigs: React.Dispatch<React.SetStateAction<InstanceConfigs[]>>;
    setInstanceRoutes: React.Dispatch<React.SetStateAction<Map<number, Map<string, Route[]>>>>;
    setSelectedRoutes: React.Dispatch<React.SetStateAction<Map<number, Map<string, Set<string>>>>>;
    setActiveConfigTab: React.Dispatch<React.SetStateAction<Map<number, string>>>;
    handleSelectionChange: (instance: number, configName: string, selectedIds: string[]) => void;
    handleConfigTabChange: (instance: number, config: string) => void;
    reloadRoutes: (instance: number, configsList: string[]) => Promise<Map<string, Route[]>>;
}

export const useRouteData = (): UseRouteDataResult => {
    const [instanceConfigs, setInstanceConfigs] = useState<InstanceConfigs[]>([]);
    const [instanceRoutes, setInstanceRoutes] = useState<Map<number, Map<string, Route[]>>>(new Map());
    const [selectedRoutes, setSelectedRoutes] = useState<Map<number, Map<string, Set<string>>>>(new Map());
    const [loading, setLoading] = useState<boolean>(true);
    const [activeConfigTab, setActiveConfigTab] = useState<Map<number, string>>(new Map());

    // Data loading
    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                const configsResponse = await API.route.listConfigs();
                const configs = configsResponse.instanceConfigs || [];

                const routesMap = new Map<number, Map<string, Route[]>>();
                const configTabsMap = new Map<number, string>();

                await Promise.all(
                    configs.map(async (instanceConfig, idx) => {
                        const instance = instanceConfig.instance ?? idx;
                        const configsList = instanceConfig.configs || [];
                        const configRoutesMap = new Map<string, Route[]>();

                        await Promise.all(
                            configsList.map(async (configName) => {
                                if (!configTabsMap.has(instance)) {
                                    configTabsMap.set(instance, configName);
                                }

                                try {
                                    const routesResponse = await API.route.showRoutes({
                                        target: {
                                            configName,
                                            dataplaneInstance: instance,
                                        },
                                    });
                                    const routes = routesResponse.routes || [];
                                    configRoutesMap.set(configName, routes);
                                } catch (err) {
                                    if (!isMounted) return;
                                    toaster.error(`route-fetch-error-${instance}-${configName}`, `Failed to load routes for ${configName} (instance ${instance})`, err);
                                }
                            })
                        );

                        routesMap.set(instance, configRoutesMap);
                    })
                );

                if (!isMounted) return;

                setInstanceConfigs(configs);
                setInstanceRoutes(routesMap);
                setActiveConfigTab(configTabsMap);
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

    const handleSelectionChange = useCallback((instance: number, configName: string, selectedIds: string[]): void => {
        setSelectedRoutes((prev) => {
            const instanceSelectedMap = prev.get(instance) || new Map<string, Set<string>>();
            const newConfigSelectedMap = new Map(instanceSelectedMap);
            newConfigSelectedMap.set(configName, new Set(selectedIds));

            const newSelected = new Map(prev);
            newSelected.set(instance, newConfigSelectedMap);
            return newSelected;
        });
    }, []);

    const handleConfigTabChange = useCallback((instance: number, config: string): void => {
        setActiveConfigTab((prev) => {
            const newMap = new Map(prev);
            newMap.set(instance, config);
            return newMap;
        });
    }, []);

    const reloadRoutes = useCallback(async (instance: number, configsList: string[]): Promise<Map<string, Route[]>> => {
        const configRoutesMap = new Map<string, Route[]>();

        for (const configName of configsList) {
            try {
                const routesResponse = await API.route.showRoutes({
                    target: {
                        configName,
                        dataplaneInstance: instance,
                    },
                });
                configRoutesMap.set(configName, routesResponse.routes || []);
            } catch (err) {
                toaster.error(`reload-route-error-${instance}-${configName}`, `Failed to reload routes for ${configName} (instance ${instance})`, err);
            }
        }

        return configRoutesMap;
    }, []);

    return {
        instanceConfigs,
        instanceRoutes,
        selectedRoutes,
        loading,
        activeConfigTab,
        setInstanceConfigs,
        setInstanceRoutes,
        setSelectedRoutes,
        setActiveConfigTab,
        handleSelectionChange,
        handleConfigTabChange,
        reloadRoutes,
    };
};
