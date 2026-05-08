import { useCallback, useEffect, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { Route } from '../../../api/routes';

export interface UseRIBDataResult {
    configRoutes: Map<string, Route[]>;
    selectedRoutes: Map<string, Set<string>>;
    setConfigRoutes: React.Dispatch<React.SetStateAction<Map<string, Route[]>>>;
    setSelectedRoutes: React.Dispatch<React.SetStateAction<Map<string, Set<string>>>>;
    handleSelectionChange: (configName: string, selectedIds: string[]) => void;
    reloadRoutes: (configsList: string[]) => Promise<Map<string, Route[]>>;
}

/** Loads and manages RIB (route information base) data for the given configs. */
export const useRIBData = (configs: string[]): UseRIBDataResult => {
    const [configRoutes, setConfigRoutes] = useState<Map<string, Route[]>>(new Map());
    const [selectedRoutes, setSelectedRoutes] = useState<Map<string, Set<string>>>(new Map());

    useEffect(() => {
        if (configs.length === 0) return;

        let isMounted = true;

        const loadAllRoutes = async (): Promise<void> => {
            const routesMap = new Map<string, Route[]>();

            await Promise.all(
                configs.map(async (configName) => {
                    try {
                        const routesResponse = await API.route.showRoutes({ name: configName });
                        routesMap.set(configName, routesResponse.routes || []);
                    } catch (err) {
                        toaster.error(`route-fetch-error-${configName}`, `Failed to load routes for ${configName}`, err);
                    }
                })
            );

            if (!isMounted) return;
            setConfigRoutes(routesMap);
        };

        loadAllRoutes();

        return () => {
            isMounted = false;
        };
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [configs.join(',')]);

    const handleSelectionChange = useCallback((configName: string, selectedIds: string[]): void => {
        setSelectedRoutes((prev) => {
            const newSelected = new Map(prev);
            newSelected.set(configName, new Set(selectedIds));
            return newSelected;
        });
    }, []);

    const reloadRoutes = useCallback(async (configsList: string[]): Promise<Map<string, Route[]>> => {
        const routesMap = new Map<string, Route[]>();

        for (const configName of configsList) {
            try {
                const routesResponse = await API.route.showRoutes({ name: configName });
                routesMap.set(configName, routesResponse.routes || []);
            } catch (err) {
                toaster.error(`reload-route-error-${configName}`, `Failed to reload routes for ${configName}`, err);
            }
        }

        return routesMap;
    }, []);

    return {
        configRoutes,
        selectedRoutes,
        setConfigRoutes,
        setSelectedRoutes,
        handleSelectionChange,
        reloadRoutes,
    };
};
