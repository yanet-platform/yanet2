import { useCallback, useEffect, useState, useRef } from 'react';
import { API } from '../../api';
import type { Rule, MapConfig, SyncConfig } from '../../api/acl';
import { toaster } from '../../utils';
import type { AclConfigData, ConfigState } from './types';

export interface UseAclDataResult {
    configs: string[];
    configData: Map<string, AclConfigData>;
    loading: boolean;
    saving: boolean;
    activeConfigTab: string;
    activeInnerTab: 'rules' | 'fwstate';
    setConfigs: React.Dispatch<React.SetStateAction<string[]>>;
    setConfigData: React.Dispatch<React.SetStateAction<Map<string, AclConfigData>>>;
    setActiveConfigTab: React.Dispatch<React.SetStateAction<string>>;
    setActiveInnerTab: React.Dispatch<React.SetStateAction<'rules' | 'fwstate'>>;
    handleConfigTabChange: (config: string) => void;
    handleInnerTabChange: (tab: 'rules' | 'fwstate') => void;
    reloadConfigs: () => Promise<void>;
    getConfigState: (configName: string) => ConfigState;
    hasUnsavedChanges: (configName: string) => boolean;
    hasAnyUnsavedChanges: () => boolean;
    markConfigModified: (configName: string) => void;
    markConfigSaved: (configName: string) => void;
    addNewConfig: (configName: string, rules: Rule[]) => void;
    updateConfigRules: (configName: string, rules: Rule[]) => void;
    updateConfigFWState: (configName: string, mapConfig?: MapConfig, syncConfig?: SyncConfig) => void;
    saveConfig: (configName: string) => Promise<boolean>;
    saveFWStateConfig: (configName: string) => Promise<boolean>;
    deleteConfig: (configName: string) => Promise<boolean>;
}

// Deep comparison for rules
const rulesEqual = (a: Rule[] | undefined, b: Rule[] | undefined): boolean => {
    if (!a && !b) return true;
    if (!a || !b) return false;
    if (a.length !== b.length) return false;
    return JSON.stringify(a) === JSON.stringify(b);
};

// Deep comparison for config objects
const configEqual = <T>(a: T | undefined, b: T | undefined): boolean => {
    if (!a && !b) return true;
    if (!a || !b) return false;
    return JSON.stringify(a) === JSON.stringify(b);
};

export const useAclData = (): UseAclDataResult => {
    const [configs, setConfigs] = useState<string[]>([]);
    const [configData, setConfigData] = useState<Map<string, AclConfigData>>(new Map());
    const [loading, setLoading] = useState<boolean>(true);
    const [saving, setSaving] = useState<boolean>(false);
    const [activeConfigTab, setActiveConfigTab] = useState<string>('');
    const [activeInnerTab, setActiveInnerTab] = useState<'rules' | 'fwstate'>('rules');

    const initialLoadDone = useRef(false);

    // Load initial data
    useEffect(() => {
        if (initialLoadDone.current) return;
        initialLoadDone.current = true;

        const loadData = async (): Promise<void> => {
            setLoading(true);

            try {
                const configsResponse = await API.acl.listConfigs();
                const configsList = configsResponse.configs || [];

                const dataMap = new Map<string, AclConfigData>();

                await Promise.all(
                    configsList.map(async (configName) => {
                        try {
                            const response = await API.acl.showConfig({
                                target: { configName },
                            });
                            const rules = response.rules || [];
                            dataMap.set(configName, {
                                rules,
                                fwstateMap: response.fwstateMap,
                                fwstateSync: response.fwstateSync,
                                state: 'saved',
                                originalRules: rules,
                                originalFwstateMap: response.fwstateMap,
                                originalFwstateSync: response.fwstateSync,
                            });
                        } catch (err) {
                            toaster.error(`acl-fetch-error-${configName}`, `Failed to load ACL config ${configName}`, err);
                        }
                    })
                );

                setConfigs(configsList);
                setConfigData(dataMap);
                if (configsList.length > 0) {
                    setActiveConfigTab(configsList[0]);
                }
            } catch (err) {
                toaster.error('acl-error', 'Failed to fetch ACL data', err);
            } finally {
                setLoading(false);
            }
        };

        loadData();
    }, []);

    const handleConfigTabChange = useCallback((config: string): void => {
        setActiveConfigTab(config);
    }, []);

    const handleInnerTabChange = useCallback((tab: 'rules' | 'fwstate'): void => {
        setActiveInnerTab(tab);
    }, []);

    const reloadConfigs = useCallback(async (): Promise<void> => {
        try {
            const configsResponse = await API.acl.listConfigs();
            const configsList = configsResponse.configs || [];

            const dataMap = new Map<string, AclConfigData>();

            await Promise.all(
                configsList.map(async (configName) => {
                    try {
                        const response = await API.acl.showConfig({
                            target: { configName },
                        });
                        const rules = response.rules || [];
                        dataMap.set(configName, {
                            rules,
                            fwstateMap: response.fwstateMap,
                            fwstateSync: response.fwstateSync,
                            state: 'saved',
                            originalRules: rules,
                            originalFwstateMap: response.fwstateMap,
                            originalFwstateSync: response.fwstateSync,
                        });
                    } catch (err) {
                        toaster.error(`acl-reload-error-${configName}`, `Failed to reload ACL config ${configName}`, err);
                    }
                })
            );

            setConfigs(configsList);
            setConfigData(dataMap);
        } catch (err) {
            toaster.error('acl-reload-error', 'Failed to reload ACL data', err);
        }
    }, []);

    const getConfigState = useCallback((configName: string): ConfigState => {
        const data = configData.get(configName);
        return data?.state || 'saved';
    }, [configData]);

    const hasUnsavedChanges = useCallback((configName: string): boolean => {
        const data = configData.get(configName);
        if (!data) return false;
        if (data.state === 'new') return true;
        if (data.state === 'modified') return true;

        // Check if current data differs from original
        if (!rulesEqual(data.rules, data.originalRules)) return true;
        if (!configEqual(data.fwstateMap, data.originalFwstateMap)) return true;
        if (!configEqual(data.fwstateSync, data.originalFwstateSync)) return true;

        return false;
    }, [configData]);

    const hasAnyUnsavedChanges = useCallback((): boolean => {
        for (const configName of configs) {
            if (hasUnsavedChanges(configName)) return true;
        }
        return false;
    }, [configs, hasUnsavedChanges]);

    const markConfigModified = useCallback((configName: string): void => {
        setConfigData((prev) => {
            const newData = new Map(prev);
            const existing = newData.get(configName);
            if (existing && existing.state !== 'new') {
                newData.set(configName, { ...existing, state: 'modified' });
            }
            return newData;
        });
    }, []);

    const markConfigSaved = useCallback((configName: string): void => {
        setConfigData((prev) => {
            const newData = new Map(prev);
            const existing = newData.get(configName);
            if (existing) {
                newData.set(configName, {
                    ...existing,
                    state: 'saved',
                    originalRules: existing.rules,
                    originalFwstateMap: existing.fwstateMap,
                    originalFwstateSync: existing.fwstateSync,
                });
            }
            return newData;
        });
    }, []);

    const addNewConfig = useCallback((configName: string, rules: Rule[]): void => {
        setConfigData((prev) => {
            const newData = new Map(prev);
            newData.set(configName, {
                rules,
                state: 'new',
                originalRules: [],
            });
            return newData;
        });
        setConfigs((prev) => {
            if (prev.includes(configName)) return prev;
            return [...prev, configName];
        });
        setActiveConfigTab(configName);
    }, []);

    const updateConfigRules = useCallback((configName: string, rules: Rule[]): void => {
        setConfigData((prev) => {
            const newData = new Map(prev);
            const existing = newData.get(configName);
            if (existing) {
                const newState: ConfigState = existing.state === 'new' ? 'new' : 'modified';
                newData.set(configName, { ...existing, rules, state: newState });
            } else {
                newData.set(configName, {
                    rules,
                    state: 'new',
                    originalRules: [],
                });
            }
            return newData;
        });
    }, []);

    const updateConfigFWState = useCallback((configName: string, mapConfig?: MapConfig, syncConfig?: SyncConfig): void => {
        setConfigData((prev) => {
            const newData = new Map(prev);
            const existing = newData.get(configName);
            if (existing) {
                const newState: ConfigState = existing.state === 'new' ? 'new' : 'modified';
                newData.set(configName, {
                    ...existing,
                    fwstateMap: mapConfig,
                    fwstateSync: syncConfig,
                    state: newState,
                });
            }
            return newData;
        });
    }, []);

    const saveConfig = useCallback(async (configName: string): Promise<boolean> => {
        const data = configData.get(configName);
        if (!data) {
            toaster.error('acl-save-error', `Config ${configName} not found`);
            return false;
        }

        setSaving(true);
        try {
            await API.acl.updateConfig({
                target: { configName },
                rules: data.rules,
            });
            markConfigSaved(configName);
            toaster.success('acl-save-success', `ACL config ${configName} saved successfully`);
            return true;
        } catch (err) {
            toaster.error('acl-save-error', `Failed to save ACL config ${configName}`, err);
            return false;
        } finally {
            setSaving(false);
        }
    }, [configData, markConfigSaved]);

    const saveFWStateConfig = useCallback(async (configName: string): Promise<boolean> => {
        const data = configData.get(configName);
        if (!data) {
            toaster.error('acl-fwstate-save-error', `Config ${configName} not found`);
            return false;
        }

        setSaving(true);
        try {
            await API.acl.updateFWStateConfig({
                target: { configName },
                mapConfig: data.fwstateMap,
                syncConfig: data.fwstateSync,
            });
            markConfigSaved(configName);
            toaster.success('acl-fwstate-save-success', `FW State config for ${configName} saved successfully`);
            return true;
        } catch (err) {
            toaster.error('acl-fwstate-save-error', `Failed to save FW State config for ${configName}`, err);
            return false;
        } finally {
            setSaving(false);
        }
    }, [configData, markConfigSaved]);

    const deleteConfig = useCallback(async (configName: string): Promise<boolean> => {
        try {
            await API.acl.deleteConfig({
                target: { configName },
            });

            setConfigData((prev) => {
                const newData = new Map(prev);
                newData.delete(configName);
                return newData;
            });

            setConfigs((prev) => prev.filter((c) => c !== configName));

            // Switch to another config if needed
            if (activeConfigTab === configName) {
                const remainingConfigs = configs.filter((c) => c !== configName);
                setActiveConfigTab(remainingConfigs[0] || '');
            }

            toaster.success('acl-delete-success', `ACL config ${configName} deleted successfully`);
            return true;
        } catch (err) {
            toaster.error('acl-delete-error', `Failed to delete ACL config ${configName}`, err);
            return false;
        }
    }, [activeConfigTab, configs]);

    return {
        configs,
        configData,
        loading,
        saving,
        activeConfigTab,
        activeInnerTab,
        setConfigs,
        setConfigData,
        setActiveConfigTab,
        setActiveInnerTab,
        handleConfigTabChange,
        handleInnerTabChange,
        reloadConfigs,
        getConfigState,
        hasUnsavedChanges,
        hasAnyUnsavedChanges,
        markConfigModified,
        markConfigSaved,
        addNewConfig,
        updateConfigRules,
        updateConfigFWState,
        saveConfig,
        saveFWStateConfig,
        deleteConfig,
    };
};

// Hook for container height measurement
export const useContainerHeight = (containerRef: React.RefObject<HTMLDivElement | null>): number => {
    const [height, setHeight] = useState(0);

    useEffect(() => {
        const updateHeight = (): void => {
            if (containerRef.current) {
                setHeight(containerRef.current.clientHeight);
            }
        };

        updateHeight();

        const resizeObserver = new ResizeObserver(updateHeight);
        if (containerRef.current) {
            resizeObserver.observe(containerRef.current);
        }

        return () => {
            resizeObserver.disconnect();
        };
    }, [containerRef]);

    return height;
};
