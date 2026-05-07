import { useState, useEffect, useCallback } from 'react';
import { API } from '../../../../api';
import type { CounterInfo, DeviceInfo } from '../../../../api';
import { useInterpolatedCounters } from '../../../../hooks';
import type { InterpolatedCounterData } from '../../../../hooks';

/** Well-known key for pipeline-level fallthrough counters. */
export const PIPELINE_COUNTER_KEY = '__pipeline__';

const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) {
        return BigInt(0);
    }
    return counter.instances.reduce((sum, inst) => {
        const instSum = (inst.values ?? []).reduce((s, val) => s + BigInt(val ?? 0), BigInt(0));
        return sum + instSum;
    }, BigInt(0));
};

const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined =>
    counters?.find(c => c.name === name);

export interface FunctionRefInfo {
    nodeId: string;
    functionName: string;
}

export interface UseFunctionRefCountersResult {
    counters: Map<string, InterpolatedCounterData>;
}

/**
 * Fetches and interpolates per-function-ref counters for a pipeline.
 *
 * Resolves all devices that have pipelineName in input_pipelines or output_pipelines,
 * then polls API.counters.function for each (device, functionName) pair each second.
 * Results are keyed by nodeId (FunctionRef.id), not by function name, so duplicate
 * function names in a pipeline each get their own bucket pointing to shared counters.
 * When refs is empty, fetches pipeline-level counters under PIPELINE_COUNTER_KEY.
 */
export const useFunctionRefCounters = (
    pipelineName: string,
    refs: FunctionRefInfo[],
): UseFunctionRefCountersResult => {
    const [devices, setDevices] = useState<DeviceInfo[]>([]);

    useEffect(() => {
        const fetchDevices = async () => {
            try {
                const response = await API.inspect.inspect();
                const allDevices = response.instance_info?.devices ?? [];

                const matchingDevices = allDevices.filter(device => {
                    const inputPipelines = device.input_pipelines ?? [];
                    const outputPipelines = device.output_pipelines ?? [];
                    return inputPipelines.some(p => p.name === pipelineName) ||
                        outputPipelines.some(p => p.name === pipelineName);
                });

                setDevices(matchingDevices);
            } catch (error) {
                console.error('Failed to fetch devices for counters:', error);
            }
        };

        fetchDevices();
    }, [pipelineName]);

    const hasFunctionRefs = refs.length > 0;

    const keys: string[] = hasFunctionRefs
        ? refs.map(r => r.nodeId)
        : [PIPELINE_COUNTER_KEY];

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        if (hasFunctionRefs) {
            for (const ref of refs) {
                newValues.set(ref.nodeId, { packets: BigInt(0), bytes: BigInt(0) });
            }

            // Build flat list of all (device, ref) pairs and fetch in parallel.
            const pairs: Array<{ deviceName: string; ref: FunctionRefInfo }> = [];
            for (const device of devices) {
                const deviceName = device.name ?? '';
                for (const ref of refs) {
                    pairs.push({ deviceName, ref });
                }
            }

            const results = await Promise.allSettled(
                pairs.map(({ deviceName, ref }) =>
                    API.counters.function({
                        device: deviceName,
                        pipeline: pipelineName,
                        function: ref.functionName,
                    }).then(response => ({ ref, response }))
                )
            );

            for (const result of results) {
                if (result.status !== 'fulfilled') continue;
                const { ref, response } = result.value;
                const rxPackets = sumCounterValues(findCounter(response.counters, 'input'));
                const rxBytes = sumCounterValues(findCounter(response.counters, 'input_bytes'));
                const current = newValues.get(ref.nodeId)!;
                newValues.set(ref.nodeId, {
                    packets: current.packets + rxPackets,
                    bytes: current.bytes + rxBytes,
                });
            }
        } else {
            newValues.set(PIPELINE_COUNTER_KEY, { packets: BigInt(0), bytes: BigInt(0) });

            // Fetch all device pipeline counters in parallel.
            const results = await Promise.allSettled(
                devices.map(device =>
                    API.counters.pipeline({
                        device: device.name ?? '',
                        pipeline: pipelineName,
                    })
                )
            );

            for (const result of results) {
                if (result.status !== 'fulfilled') continue;
                const response = result.value;
                const rxPackets = sumCounterValues(findCounter(response.counters, 'input'));
                const rxBytes = sumCounterValues(findCounter(response.counters, 'input_bytes'));
                const current = newValues.get(PIPELINE_COUNTER_KEY)!;
                newValues.set(PIPELINE_COUNTER_KEY, {
                    packets: current.packets + rxPackets,
                    bytes: current.bytes + rxBytes,
                });
            }
        }

        return newValues;
    }, [devices, pipelineName, refs, hasFunctionRefs]);

    const { counters } = useInterpolatedCounters({
        keys,
        fetchCounters,
        enabled: devices.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    return { counters };
};
