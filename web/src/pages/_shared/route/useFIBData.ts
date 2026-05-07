import { useCallback, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { FIBEntry } from '../../../api/routes';

export interface UseFIBDataResult {
    configFIB: Map<string, FIBEntry[]>;
    setConfigFIB: React.Dispatch<React.SetStateAction<Map<string, FIBEntry[]>>>;
    reloadFIB: (configsList: string[]) => Promise<Map<string, FIBEntry[]>>;
    loadFIBForConfig: (configName: string) => Promise<FIBEntry[]>;
}

/** Lazily loads and caches FIB (forwarding information base) entries per config. */
export const useFIBData = (_configs: string[]): UseFIBDataResult => {
    const [configFIB, setConfigFIB] = useState<Map<string, FIBEntry[]>>(new Map());

    const reloadFIB = useCallback(async (configsList: string[]): Promise<Map<string, FIBEntry[]>> => {
        const fibMap = new Map<string, FIBEntry[]>();

        for (const configName of configsList) {
            try {
                const fibResponse = await API.route.showFIB({ name: configName });
                fibMap.set(configName, fibResponse.entries || []);
            } catch (err) {
                toaster.error(`reload-fib-error-${configName}`, `Failed to reload FIB for ${configName}`, err);
            }
        }

        return fibMap;
    }, []);

    const loadFIBForConfig = useCallback(async (configName: string): Promise<FIBEntry[]> => {
        const cached = configFIB.get(configName);
        if (cached) {
            return cached;
        }

        try {
            const fibResponse = await API.route.showFIB({ name: configName });
            const entries = fibResponse.entries || [];
            setConfigFIB((prev) => {
                const next = new Map(prev);
                next.set(configName, entries);
                return next;
            });
            return entries;
        } catch (err) {
            toaster.error(`load-fib-error-${configName}`, `Failed to load FIB for ${configName}`, err);
            return [];
        }
    }, [configFIB]);

    return {
        configFIB,
        setConfigFIB,
        reloadFIB,
        loadFIBForConfig,
    };
};
