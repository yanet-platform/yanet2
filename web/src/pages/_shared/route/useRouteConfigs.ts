import { useCallback, useEffect, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';

export interface UseRouteConfigsResult {
    configs: string[];
    loading: boolean;
    activeConfigTab: string;
    setConfigs: React.Dispatch<React.SetStateAction<string[]>>;
    setActiveConfigTab: React.Dispatch<React.SetStateAction<string>>;
    handleConfigTabChange: (config: string) => void;
}

/** Loads the list of route configs and manages the active tab selection. */
export const useRouteConfigs = (): UseRouteConfigsResult => {
    const [configs, setConfigs] = useState<string[]>([]);
    const [loading, setLoading] = useState<boolean>(true);
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');

    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                const configsResponse = await API.route.listConfigs();
                const configsList = configsResponse.configs || [];

                if (!isMounted) return;

                setConfigs(configsList);
                if (configsList.length > 0) {
                    setActiveConfigTab(configsList[0]);
                }
            } catch (err) {
                if (!isMounted) return;
                toaster.error('route-configs-error', 'Failed to fetch route configs', err);
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

    const handleConfigTabChange = useCallback((config: string): void => {
        setActiveConfigTab(config);
    }, []);

    return {
        configs,
        loading,
        activeConfigTab,
        setConfigs,
        setActiveConfigTab,
        handleConfigTabChange,
    };
};
