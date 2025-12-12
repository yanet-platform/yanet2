import { useCallback, useEffect, useState } from 'react';
import { API } from '../../api';
import { toaster } from '../../utils';
import type { DecapInstanceData } from './types';

export interface UseDecapDataResult {
    instances: DecapInstanceData[];
    loading: boolean;
    selectedPrefixes: Map<number, Map<string, Set<string>>>;
    setSelectedPrefixes: React.Dispatch<React.SetStateAction<Map<number, Map<string, Set<string>>>>>;
    handleSelectionChange: (instance: number, configName: string, selectedIds: Set<string>) => void;
    addConfig: (instance: number, configName: string) => void;
    addPrefixes: (instance: number, configName: string, prefixes: string[]) => Promise<boolean>;
    removePrefixes: (instance: number, configName: string, prefixes: string[]) => Promise<boolean>;
    reloadConfig: (instance: number, configName: string) => Promise<void>;
}

export const useDecapData = (): UseDecapDataResult => {
    const [instances, setInstances] = useState<DecapInstanceData[]>([]);
    const [loading, setLoading] = useState<boolean>(true);
    const [selectedPrefixes, setSelectedPrefixes] = useState<Map<number, Map<string, Set<string>>>>(new Map());

    // Load data for all instances
    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                // First get list of all instances and their decap configs via Inspect API
                const inspectResponse = await API.inspect.inspect();
                
                if (!isMounted) return;

                const instanceInfos = inspectResponse.instanceInfo || [];
                const loadedInstances: DecapInstanceData[] = [];

                for (const info of instanceInfos) {
                    const instanceIdx = info.instanceIdx ?? 0;
                    
                    // Find decap configs for this instance
                    const decapConfigs = (info.cpConfigs || [])
                        .filter((cfg) => cfg.type === 'decap')
                        .map((cfg) => cfg.name || '');

                    const configPrefixes = new Map<string, string[]>();

                    // Load prefixes for each decap config
                    for (const configName of decapConfigs) {
                        if (!configName) continue;

                        try {
                            const response = await API.decap.showConfig({
                                target: {
                                    configName,
                                    dataplaneInstance: instanceIdx,
                                },
                            });

                            const prefixes = response.config?.prefixes || [];
                            configPrefixes.set(configName, prefixes);
                        } catch (err) {
                            // Config might not exist yet, just log and continue
                            configPrefixes.set(configName, []);
                        }
                    }

                    loadedInstances.push({
                        instance: instanceIdx,
                        configs: decapConfigs,
                        configPrefixes,
                    });
                }

                if (!isMounted) return;

                // If no instances found, create a default empty one
                if (loadedInstances.length === 0) {
                    loadedInstances.push({
                        instance: 0,
                        configs: [],
                        configPrefixes: new Map(),
                    });
                }

                setInstances(loadedInstances);
            } catch (err) {
                if (!isMounted) return;
                toaster.error('decap-load-error', 'Failed to load decap configuration', err);
                // Still set empty instance so UI can be shown
                setInstances([{
                    instance: 0,
                    configs: [],
                    configPrefixes: new Map(),
                }]);
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

    const handleSelectionChange = useCallback((instance: number, configName: string, selectedIds: Set<string>): void => {
        setSelectedPrefixes((prev) => {
            const newMap = new Map(prev);
            const instanceMap = newMap.get(instance) || new Map<string, Set<string>>();
            const newInstanceMap = new Map(instanceMap);
            newInstanceMap.set(configName, selectedIds);
            newMap.set(instance, newInstanceMap);
            return newMap;
        });
    }, []);

    const addConfig = useCallback((instance: number, configName: string): void => {
        setInstances((prev) => {
            return prev.map((inst) => {
                if (inst.instance === instance) {
                    // Don't add if already exists
                    if (inst.configs.includes(configName)) {
                        return inst;
                    }
                    const newConfigPrefixes = new Map(inst.configPrefixes);
                    newConfigPrefixes.set(configName, []);
                    return {
                        ...inst,
                        configs: [...inst.configs, configName],
                        configPrefixes: newConfigPrefixes,
                    };
                }
                return inst;
            });
        });
    }, []);

    const reloadConfig = useCallback(async (instance: number, configName: string): Promise<void> => {
        try {
            const response = await API.decap.showConfig({
                target: {
                    configName,
                    dataplaneInstance: instance,
                },
            });

            const prefixes = response.config?.prefixes || [];
            setInstances((prev) => {
                return prev.map((inst) => {
                    if (inst.instance === instance) {
                        const newConfigPrefixes = new Map(inst.configPrefixes);
                        newConfigPrefixes.set(configName, prefixes);
                        return {
                            ...inst,
                            configPrefixes: newConfigPrefixes,
                        };
                    }
                    return inst;
                });
            });
        } catch (err) {
            toaster.error(`decap-reload-error-${instance}-${configName}`, `Failed to reload decap config ${configName}`, err);
        }
    }, []);

    const addPrefixes = useCallback(async (instance: number, configName: string, prefixes: string[]): Promise<boolean> => {
        try {
            await API.decap.addPrefixes({
                target: {
                    configName,
                    dataplaneInstance: instance,
                },
                prefixes,
            });

            await reloadConfig(instance, configName);
            toaster.success('decap-add-success', `Added ${prefixes.length} prefix(es)`);
            return true;
        } catch (err) {
            toaster.error('decap-add-error', 'Failed to add prefixes', err);
            return false;
        }
    }, [reloadConfig]);

    const removePrefixes = useCallback(async (instance: number, configName: string, prefixes: string[]): Promise<boolean> => {
        try {
            await API.decap.removePrefixes({
                target: {
                    configName,
                    dataplaneInstance: instance,
                },
                prefixes,
            });

            await reloadConfig(instance, configName);
            
            // Clear selection for removed prefixes
            setSelectedPrefixes((prev) => {
                const newMap = new Map(prev);
                const instanceMap = newMap.get(instance) || new Map<string, Set<string>>();
                const currentSelection = instanceMap.get(configName) || new Set();
                const newSelection = new Set<string>();
                currentSelection.forEach((id) => {
                    if (!prefixes.includes(id)) {
                        newSelection.add(id);
                    }
                });
                const newInstanceMap = new Map(instanceMap);
                newInstanceMap.set(configName, newSelection);
                newMap.set(instance, newInstanceMap);
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
        instances,
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
