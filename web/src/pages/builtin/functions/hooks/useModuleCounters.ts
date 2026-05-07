import { useState, useEffect, useCallback } from 'react';
import { API } from '../../../../api';
import type { CounterInfo, DeviceInfo } from '../../../../api';
import { useInterpolatedCounters } from '../../../../hooks';
import type { InterpolatedCounterData } from '../../../../hooks';

const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) return BigInt(0);
    return counter.instances.reduce((sum, inst) => {
        const instSum = (inst.values ?? []).reduce((s, val) => s + BigInt(val ?? 0), BigInt(0));
        return sum + instSum;
    }, BigInt(0));
};

const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined => {
    return counters?.find(c => c.name === name);
};

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
 * Uses the generic useInterpolatedCounters hook with module-specific fetch logic.
 * Polls module counters every 1 second from backend using the Module API.
 * Aggregates counters across all devices and pipelines using the function.
 * Updates visual every 30ms using linear interpolation.
 */
export const useModuleCounters = (
    functionName: string,
    moduleInfoList: ModuleInfo[]
): UseModuleCountersResult => {
    const [devices, setDevices] = useState<DeviceInfo[]>([]);
    const [pipelineNames, setPipelineNames] = useState<string[]>([]);

    useEffect(() => {
        const fetchDevicesAndPipelines = async () => {
            try {
                const response = await API.inspect.inspect();
                const instanceInfo = response.instance_info;
                const allDevices = instanceInfo?.devices ?? [];
                const allPipelines = instanceInfo?.pipelines ?? [];

                const matchingPipelines = allPipelines.filter(p => {
                    const funcs = p.functions ?? [];
                    return funcs.includes(functionName);
                });

                const pipelineNamesSet = new Set(matchingPipelines.map(p => p.name).filter((n): n is string => !!n));

                const matchingDevices: DeviceInfo[] = [];
                for (const device of allDevices) {
                    const inputPipelines = device.input_pipelines ?? [];
                    const outputPipelines = device.output_pipelines ?? [];
                    const allDevicePipelines = [...inputPipelines, ...outputPipelines];

                    for (const pipeline of allDevicePipelines) {
                        if (pipeline.name && pipelineNamesSet.has(pipeline.name)) {
                            if (!matchingDevices.includes(device)) {
                                matchingDevices.push(device);
                            }
                        }
                    }
                }

                setDevices(matchingDevices);
                setPipelineNames(Array.from(pipelineNamesSet));
            } catch (error) {
                console.error('Failed to fetch devices for counters:', error);
            }
        };

        fetchDevicesAndPipelines();
    }, [functionName]);

    const nodeIds = moduleInfoList.map(m => m.nodeId);

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        for (const moduleInfo of moduleInfoList) {
            newValues.set(moduleInfo.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
        }

        for (const device of devices) {
            const deviceName = device.name || '';

            for (const pipelineName of pipelineNames) {
                for (const moduleInfo of moduleInfoList) {
                    try {
                        const response = await API.counters.module({
                            device: deviceName,
                            pipeline: pipelineName,
                            function: functionName,
                            chain: moduleInfo.chainName,
                            module_type: moduleInfo.moduleType,
                            module_name: moduleInfo.moduleName,
                            counter_query: ['rx', 'rx_bytes'],
                        });

                        const rxPackets = sumCounterValues(findCounter(response.counters, 'rx'));
                        const rxBytes = sumCounterValues(findCounter(response.counters, 'rx_bytes'));

                        const current = newValues.get(moduleInfo.nodeId)!;
                        newValues.set(moduleInfo.nodeId, {
                            packets: current.packets + rxPackets,
                            bytes: current.bytes + rxBytes,
                        });
                    } catch {
                        // Ignore errors for individual module counters.
                    }
                }
            }
        }

        return newValues;
    }, [devices, pipelineNames, functionName, moduleInfoList]);

    const { counters } = useInterpolatedCounters({
        keys: nodeIds,
        fetchCounters,
        enabled: devices.length > 0 && pipelineNames.length > 0 && moduleInfoList.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
