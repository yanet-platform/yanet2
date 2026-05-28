import { useCallback } from 'react';
import { API } from '../../../../api';
import { useInterpolatedCounters } from '../../../../hooks';
import type { InterpolatedCounterData } from '../../../../hooks';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../../utils';

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
                tags: [{ key: 'module_type', value: '*' }],
                query: ['rx', 'rx_bytes'],
            });
            const grouped = groupCounterGroupsByTagsAndName(
                response.groups,
                ['function', 'chain', 'module_type', 'module_name'],
                0
            );

            for (const moduleInfo of moduleInfoList) {
                const keyPrefix = [
                    functionName,
                    moduleInfo.chainName,
                    moduleInfo.moduleType,
                    moduleInfo.moduleName,
                ];
                const rxPackets = grouped.get(makeGroupedCounterKey(keyPrefix, 'rx'))?.value ?? BigInt(0);
                const rxBytes = grouped.get(makeGroupedCounterKey(keyPrefix, 'rx_bytes'))?.value ?? BigInt(0);

                newValues.set(moduleInfo.nodeId, {
                    packets: rxPackets,
                    bytes: rxBytes,
                });
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
