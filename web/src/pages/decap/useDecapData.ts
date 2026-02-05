import { useCallback, useEffect, useState } from 'react';
import { API } from '../../api';
import { toaster } from '../../utils';

export interface DecapConfigData {
    configs: string[];
    configPrefixes: Map<string, string[]>;
}

export interface UseDecapDataResult {
    data: DecapConfigData;
    loading: boolean;
    selectedPrefixes: Map<string, Set<string>>;
    setSelectedPrefixes: React.Dispatch<React.SetStateAction<Map<string, Set<string>>>>;
    handleSelectionChange: (configName: string, selectedIds: Set<string>) => void;
    addConfig: (configName: string) => void;
    addPrefixes: (configName: string, prefixes: string[]) => Promise<boolean>;
    removePrefixes: (configName: string, prefixes: string[]) => Promise<boolean>;
    reloadConfig: (configName: string) => Promise<void>;
}

export const useDecapData = (): UseDecapDataResult => {
    const [data, setData] = useState<DecapConfigData>({ configs: [], configPrefixes: new Map() });
    const [loading, setLoading] = useState<boolean>(true);
    const [selectedPrefixes, setSelectedPrefixes] = useState<Map<string, Set<string>>>(new Map());

    // Load data
    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                // First get list of decap configs via Inspect API
                const inspectResponse = await API.inspect.inspect();

                if (!isMounted) return;

                const info = inspectResponse.instance_info;

                // Find decap configs
                const decapConfigs = (info?.cp_configs || [])
                    .filter((cfg) => cfg.type === 'decap')
                    .map((cfg) => cfg.name || '');

                const configPrefixes = new Map<string, string[]>();

                // Load prefixes for each decap config
                for (const configName of decapConfigs) {
                    if (!configName) continue;

                    try {
                        const response = await API.decap.showConfig({
                            name: configName,
                        });

                        const prefixes = response.prefixes || [];
                        configPrefixes.set(configName, prefixes);
                    } catch (err) {
                        // Config might not exist yet, just log and continue
                        configPrefixes.set(configName, []);
                    }
                }

                if (!isMounted) return;

                setData({
                    configs: decapConfigs,
                    configPrefixes,
                });
            } catch (err) {
                if (!isMounted) return;
                toaster.error('decap-load-error', 'Failed to load decap configuration', err);
                // Still set empty data so UI can be shown
                setData({
                    configs: [],
                    configPrefixes: new Map(),
                });
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

    const handleSelectionChange = useCallback((configName: string, selectedIds: Set<string>): void => {
        setSelectedPrefixes((prev) => {
            const newMap = new Map(prev);
            newMap.set(configName, selectedIds);
            return newMap;
        });
    }, []);

    const addConfig = useCallback((configName: string): void => {
        setData((prev) => {
            // Don't add if already exists
            if (prev.configs.includes(configName)) {
                return prev;
            }
            const newConfigPrefixes = new Map(prev.configPrefixes);
            newConfigPrefixes.set(configName, []);
            return {
                configs: [...prev.configs, configName],
                configPrefixes: newConfigPrefixes,
            };
        });
    }, []);

    const reloadConfig = useCallback(async (configName: string): Promise<void> => {
        try {
            const response = await API.decap.showConfig({
                name: configName,
            });

            const prefixes = response.prefixes || [];
            setData((prev) => {
                const newConfigPrefixes = new Map(prev.configPrefixes);
                newConfigPrefixes.set(configName, prefixes);
                return {
                    ...prev,
                    configPrefixes: newConfigPrefixes,
                };
            });
        } catch (err) {
            toaster.error(`decap-reload-error-${configName}`, `Failed to reload decap config ${configName}`, err);
        }
    }, []);

    const addPrefixes = useCallback(async (configName: string, prefixes: string[]): Promise<boolean> => {
        try {
            await API.decap.addPrefixes({
                name: configName,
                prefixes,
            });

            await reloadConfig(configName);
            toaster.success('decap-add-success', `Added ${prefixes.length} prefix(es)`);
            return true;
        } catch (err) {
            toaster.error('decap-add-error', 'Failed to add prefixes', err);
            return false;
        }
    }, [reloadConfig]);

    const removePrefixes = useCallback(async (configName: string, prefixes: string[]): Promise<boolean> => {
        try {
            await API.decap.removePrefixes({
                name: configName,
                prefixes,
            });

            await reloadConfig(configName);

            // Clear selection for removed prefixes
            setSelectedPrefixes((prev) => {
                const currentSelection = prev.get(configName) || new Set();
                const newSelection = new Set<string>();
                currentSelection.forEach((id) => {
                    if (!prefixes.includes(id)) {
                        newSelection.add(id);
                    }
                });
                const newMap = new Map(prev);
                newMap.set(configName, newSelection);
                return newMap;
            });

            toaster.success('decap-remove-success', `Removed ${prefixes.length} prefix(es)`);
            return true;
        } catch (err) {
            toaster.error('decap-remove-error', 'Failed to remove prefixes', err);
            return false;
        }
    }, [reloadConfig]);

    return {
        data,
        loading,
        selectedPrefixes,
        setSelectedPrefixes,
        handleSelectionChange,
        addConfig,
        addPrefixes,
        removePrefixes,
        reloadConfig,
    };
};
