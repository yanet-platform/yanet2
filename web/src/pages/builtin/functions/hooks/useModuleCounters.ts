import { useState, useEffect, useCallback } from 'react';
import { API } from '../../../../api';
import type { CounterInfo, DeviceInfo } from '../../../../api';
import { useInterpolatedCounters } from '../../../../hooks';
import type { InterpolatedCounterData } from '../../../../hooks';

const DEFAULT_INTERVAL_MS = 1500;

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
 *
 * The topology (device and pipeline list) is re-fetched at DEFAULT_INTERVAL_MS
 * so that adding or removing a device is reflected without a page reload.
 */
export const useModuleCounters = (
    functionName: string,
    moduleInfoList: ModuleInfo[]
): UseModuleCountersResult => {
    const [devices, setDevices] = useState<DeviceInfo[]>([]);
    const [pipelineNames, setPipelineNames] = useState<string[]>([]);

    useEffect(() => {
        let cancelled = false;

        const fetchDevicesAndPipelines = async (): Promise<void> => {
            try {
                const response = await API.inspect.inspect();
                if (cancelled) return;
                const instanceInfo = response.instance_info;
                const allDevices = instanceInfo?.devices ?? [];
                const allPipelines = instanceInfo?.pipelines ?? [];

                const matchingPipelines = allPipelines.filter(p => {
                    const funcs = p.functions ?? [];
                    return funcs.includes(functionName);
                });

                const pipelineNamesSet = new Set(matchingPipelines.map(p => p.name).filter((n): n is string => !!n));

                const matchingDeviceNames = new Set<string>();
                const matchingDevices: DeviceInfo[] = [];
                for (const device of allDevices) {
                    const inputPipelines = device.input_pipelines ?? [];
                    const outputPipelines = device.output_pipelines ?? [];
                    const allDevicePipelines = [...inputPipelines, ...outputPipelines];

                    for (const pipeline of allDevicePipelines) {
                        if (pipeline.name && pipelineNamesSet.has(pipeline.name)) {
                            const devName = device.name ?? '';
                            if (!matchingDeviceNames.has(devName)) {
                                matchingDeviceNames.add(devName);
                                matchingDevices.push(device);
                            }
                        }
                    }
                }

                setDevices(matchingDevices);
                setPipelineNames(Array.from(pipelineNamesSet));
            } catch {
                // Topology fetch failures are non-fatal; counters just stay empty.
            }
        };

        fetchDevicesAndPipelines();
        const id = setInterval(fetchDevicesAndPipelines, DEFAULT_INTERVAL_MS);
        return () => {
            cancelled = true;
            clearInterval(id);
        };
    }, [functionName]);

    const nodeIds = moduleInfoList.map(m => m.nodeId);

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        for (const moduleInfo of moduleInfoList) {
            newValues.set(moduleInfo.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
        }

        // Build flat list of all (device, pipeline, moduleInfo) triples and fetch in parallel.
        const triples: Array<{ deviceName: string; pipelineName: string; moduleInfo: ModuleInfo }> = [];
        for (const device of devices) {
            const deviceName = device.name || '';
            for (const pipelineName of pipelineNames) {
                for (const moduleInfo of moduleInfoList) {
                    triples.push({ deviceName, pipelineName, moduleInfo });
                }
            }
        }

        const results = await Promise.allSettled(
            triples.map(({ deviceName, pipelineName, moduleInfo }) =>
                API.counters.module({
                    device: deviceName,
                    pipeline: pipelineName,
                    function: functionName,
                    chain: moduleInfo.chainName,
                    module_type: moduleInfo.moduleType,
                    module_name: moduleInfo.moduleName,
                    counter_query: ['rx', 'rx_bytes'],
                }).then(response => ({ moduleInfo, response }))
            )
        );

        for (const result of results) {
            if (result.status !== 'fulfilled') continue;
            const { moduleInfo, response } = result.value;
            const rxPackets = sumCounterValues(findCounter(response.counters, 'rx'));
            const rxBytes = sumCounterValues(findCounter(response.counters, 'rx_bytes'));
            const current = newValues.get(moduleInfo.nodeId) ?? { packets: BigInt(0), bytes: BigInt(0) };
            newValues.set(moduleInfo.nodeId, {
                packets: current.packets + rxPackets,
                bytes: current.bytes + rxBytes,
            });
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
