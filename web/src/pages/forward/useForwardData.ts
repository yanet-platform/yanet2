import { useCallback, useEffect, useState } from 'react';
import { API } from '../../api';
import { toaster } from '../../utils';
import type { Rule } from '../../api/forward';

export interface ForwardConfigData {
    configs: string[];
    configRules: Map<string, Rule[]>;
}

export interface UseForwardDataResult {
    data: ForwardConfigData;
    loading: boolean;
    selectedRules: Map<string, Set<string>>;
    setSelectedRules: React.Dispatch<React.SetStateAction<Map<string, Set<string>>>>;
    handleSelectionChange: (configName: string, selectedIds: Set<string>) => void;
    addConfig: (configName: string) => void;
    addRule: (configName: string, rule: Rule) => Promise<boolean>;
    updateRule: (configName: string, ruleIndex: number, rule: Rule) => Promise<boolean>;
    removeRules: (configName: string, ruleIndices: number[]) => Promise<boolean>;
    reloadConfig: (configName: string) => Promise<void>;
    deleteConfig: (configName: string) => Promise<boolean>;
}

export const useForwardData = (): UseForwardDataResult => {
    const [data, setData] = useState<ForwardConfigData>({ configs: [], configRules: new Map() });
    const [loading, setLoading] = useState<boolean>(true);
    const [selectedRules, setSelectedRules] = useState<Map<string, Set<string>>>(new Map());

    // Load data
    useEffect(() => {
        let isMounted = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                // First get list of forward configs via Inspect API
                const inspectResponse = await API.inspect.inspect();

                if (!isMounted) return;

                const info = inspectResponse.instance_info;

                // Find forward configs
                const forwardConfigs = (info?.cp_configs || [])
                    .filter((cfg) => cfg.type === 'forward')
                    .map((cfg) => cfg.name || '');

                const configRules = new Map<string, Rule[]>();

                // Load rules for each forward config
                for (const configName of forwardConfigs) {
                    if (!configName) continue;

                    try {
                        const response = await API.forward.showConfig({
                            name: configName,
                        });

                        const rules = response.rules || [];
                        configRules.set(configName, rules);
                    } catch {
                        // Config might not exist yet, just log and continue
                        configRules.set(configName, []);
                    }
                }

                if (!isMounted) return;

                setData({
                    configs: forwardConfigs,
                    configRules,
                });
            } catch (err) {
                if (!isMounted) return;
                toaster.error('forward-load-error', 'Failed to load forward configuration', err);
                // Still set empty data so UI can be shown
                setData({
                    configs: [],
                    configRules: new Map(),
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
        setSelectedRules((prev) => {
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
            const newConfigRules = new Map(prev.configRules);
            newConfigRules.set(configName, []);
            return {
                configs: [...prev.configs, configName],
                configRules: newConfigRules,
            };
        });
    }, []);

    const reloadConfig = useCallback(async (configName: string): Promise<void> => {
        try {
            const response = await API.forward.showConfig({
                name: configName,
            });

            const rules = response.rules || [];
            setData((prev) => {
                const newConfigRules = new Map(prev.configRules);
                newConfigRules.set(configName, rules);
                return {
                    ...prev,
                    configRules: newConfigRules,
                };
            });
        } catch (err) {
            toaster.error(`forward-reload-error-${configName}`, `Failed to reload forward config ${configName}`, err);
        }
    }, []);

    const saveConfig = useCallback(async (configName: string, rules: Rule[]): Promise<boolean> => {
        try {
            await API.forward.updateConfig({
                name: configName,
                rules,
            });
            return true;
        } catch (err) {
            toaster.error('forward-save-error', 'Failed to save forward configuration', err);
            return false;
        }
    }, []);

    const addRule = useCallback(async (configName: string, rule: Rule): Promise<boolean> => {
        // Get current rules and add new one
        const currentRules = data.configRules.get(configName) || [];
        const newRules = [...currentRules, rule];

        const success = await saveConfig(configName, newRules);
        if (success) {
            await reloadConfig(configName);
            toaster.success('forward-add-success', 'Rule added successfully');
        }
        return success;
    }, [data.configRules, saveConfig, reloadConfig]);

    const updateRule = useCallback(async (configName: string, ruleIndex: number, rule: Rule): Promise<boolean> => {
        // Get current rules and update the one at index
        const currentRules = data.configRules.get(configName) || [];
        const newRules = [...currentRules];
        newRules[ruleIndex] = rule;

        const success = await saveConfig(configName, newRules);
        if (success) {
            await reloadConfig(configName);
            toaster.success('forward-update-success', 'Rule updated successfully');
        }
        return success;
    }, [data.configRules, saveConfig, reloadConfig]);

    const removeRules = useCallback(async (configName: string, ruleIndices: number[]): Promise<boolean> => {
        // Get current rules and remove the ones at indices
        const currentRules = data.configRules.get(configName) || [];
        const indicesToRemove = new Set(ruleIndices);
        const newRules = currentRules.filter((_, idx) => !indicesToRemove.has(idx));

        const success = await saveConfig(configName, newRules);
        if (success) {
            await reloadConfig(configName);

            // Clear selection for removed rules
            setSelectedRules((prev) => {
                const newMap = new Map(prev);
                newMap.set(configName, new Set());
                return newMap;
            });

            toaster.success('forward-remove-success', `Removed ${ruleIndices.length} rule(s)`);
        }
        return success;
    }, [data.configRules, saveConfig, reloadConfig]);

    const deleteConfig = useCallback(async (configName: string): Promise<boolean> => {
        try {
            const response = await API.forward.deleteConfig({ name: configName });
            if (response.deleted) {
                setData((prev) => {
                    const newConfigs = prev.configs.filter((c) => c !== configName);
                    const newConfigRules = new Map(prev.configRules);
                    newConfigRules.delete(configName);
                    return {
                        configs: newConfigs,
                        configRules: newConfigRules,
                    };
                });
                setSelectedRules((prev) => {
                    const newMap = new Map(prev);
                    newMap.delete(configName);
                    return newMap;
                });
                toaster.success('forward-delete-success', `Config "${configName}" deleted`);
                return true;
            }
            toaster.error('forward-delete-error', `Failed to delete config "${configName}"`);
            return false;
        } catch (err) {
            toaster.error('forward-delete-error', 'Failed to delete forward configuration', err);
            return false;
        }
    }, []);

    return {
        data,
        loading,
        selectedRules,
        setSelectedRules,
        handleSelectionChange,
        addConfig,
        addRule,
        updateRule,
        removeRules,
        reloadConfig,
        deleteConfig,
    };
};
