import { useCallback } from 'react';
import { API } from '@yanet/core/api';
import { useInterpolatedCounters } from '@yanet/core/hooks';
import type { InterpolatedCounterData } from '@yanet/core/hooks';
import { groupCounterPacketsAndBytes, makeGroupedCounterKey } from '@yanet/core/utils';

export interface ModuleInfo {
    nodeId: string;
    chainName: string;
    moduleType: string;
    moduleName: string;
}

export interface UseModuleCountersResult {
    counters: Map<string, InterpolatedCounterData>;
}

/**
 * Hook for fetching and interpolating module counters.
 *
 * Polls module counters every 1 second from backend using the ByTags API
 * and updates visual every 30ms using linear interpolation.
 */
export const useModuleCounters = (
    functionName: string,
    moduleInfoList: ModuleInfo[]
): UseModuleCountersResult => {
    const nodeIds = moduleInfoList.map(m => m.nodeId);

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        for (const moduleInfo of moduleInfoList) {
            newValues.set(moduleInfo.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
        }

        if (!functionName || moduleInfoList.length === 0) {
            return newValues;
        }

        try {
            const response = await API.counters.byTags({
                tags: [{ key: 'kind', value: 'module' }],
                query: ['rx'],
            });
            const grouped = groupCounterPacketsAndBytes(
                response.groups,
                ['function', 'chain', 'module_type', 'module_name'],
            );

            for (const moduleInfo of moduleInfoList) {
                const keyPrefix = [
                    functionName,
                    moduleInfo.chainName,
                    moduleInfo.moduleType,
                    moduleInfo.moduleName,
                ];
                const rx = grouped.get(makeGroupedCounterKey(keyPrefix, 'rx')) ?? { packets: BigInt(0), bytes: BigInt(0) };

                newValues.set(moduleInfo.nodeId, rx);
            }
        } catch {
            // tolerate fetch failures.
        }

        return newValues;
    }, [functionName, moduleInfoList]);

    const { counters } = useInterpolatedCounters({
        keys: nodeIds,
        fetchCounters,
        enabled: functionName.length > 0 && moduleInfoList.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
