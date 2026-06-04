import { useMemo } from 'react';
import { API } from '../../../api';
import type { DeviceCounterData } from '../../../hooks';
import { useInterpolatedCounters } from '../../../hooks/useInterpolatedCounters';
import { useRollingWindow } from '../../../hooks/useRollingWindow';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../utils';

const DEFAULT_INTERVAL_MS = 1500;
const DEFAULT_MAX_LEN = 30;

/**
 * Push the current value onto a rolling history at the polling interval.
 *
 * Thin wrapper over useRollingWindow for a single scalar value.
 */
export const useRollingSeries = (
    value: number | undefined,
    maxLen: number = DEFAULT_MAX_LEN,
    intervalMs: number = DEFAULT_INTERVAL_MS,
): number[] => {
    const key = '__single__';
    const samples = useMemo(() => {
        const m = new Map<string, number>();
        m.set(key, value ?? 0);
        return m;
    }, [value]);
    const window = useRollingWindow(samples, maxLen, intervalMs);
    return window.get(key) ?? [];
};

/**
 * Aggregate device pps over physical devices only and produce a rolling
 * throughput series. Restricting to physical devices avoids double-counting
 * traffic that also appears on stacked virtual devices (e.g. vlan).
 */
export const useThroughputSeries = (
    deviceCounters: Map<string, DeviceCounterData>,
    physicalDeviceNames: Set<string>,
    maxLen: number = DEFAULT_MAX_LEN,
): { current: number; series: number[] } => {
    let current = 0;
    deviceCounters.forEach((d, name) => {
        if (!physicalDeviceNames.has(name)) return;
        current += (d.rx?.pps ?? 0) + (d.tx?.pps ?? 0);
    });
    const series = useRollingSeries(current, maxLen);
    return { current, series };
};

/**
 * Per-device rolling pps series for a given direction.
 */
export const useDeviceTrendSeries = (
    deviceCounters: Map<string, DeviceCounterData>,
    kind: 'rx' | 'tx',
    maxLen: number = DEFAULT_MAX_LEN,
): Map<string, number[]> => {
    const samples = useMemo(() => {
        const m = new Map<string, number>();
        deviceCounters.forEach((d, name) => {
            m.set(name, (kind === 'rx' ? d.rx?.pps : d.tx?.pps) ?? 0);
        });
        return m;
    }, [deviceCounters, kind]);
    return useRollingWindow(samples, maxLen, DEFAULT_INTERVAL_MS);
};

interface RatesAndSeries {
    rates: Map<string, { pps: number; bps: number }>;
    series: Map<string, number[]>;
}

/**
 * Poll pipeline counters via tag selection and produce per-pipeline
 * rate and rolling series.
 */
export const usePipelineCounters = (
    _devices: string[],
    pipelines: string[],
    enabled: boolean,
): RatesAndSeries => {
    const pipelinesKey = useMemo(() => pipelines.join('|'), [pipelines]);

    const fetchCounters = useMemo(() => async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const totals = new Map<string, { packets: bigint; bytes: bigint }>();
        for (const p of pipelines) {
            totals.set(p, { packets: BigInt(0), bytes: BigInt(0) });
        }

        try {
            const response = await API.counters.byTags({
                tags: [
                    { key: 'pipeline', value: '*' },
                    { key: 'function', value: '' },
                ],
                query: ['input', 'input_bytes'],
            });
            const grouped = groupCounterGroupsByTagsAndName(response.groups, ['pipeline'], 0);
            for (const pipeline of pipelines) {
                totals.set(pipeline, {
                    packets: grouped.get(makeGroupedCounterKey([pipeline], 'input'))?.value ?? BigInt(0),
                    bytes: grouped.get(makeGroupedCounterKey([pipeline], 'input_bytes'))?.value ?? BigInt(0),
                });
            }
        } catch {
            // tolerate fetch failures.
        }

        return totals;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [pipelinesKey]);

    const { counters } = useInterpolatedCounters({
        keys: pipelines,
        fetchCounters,
        enabled: enabled && pipelines.length > 0,
        pollingInterval: DEFAULT_INTERVAL_MS,
    });

    const ppsSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((r, name) => m.set(name, r.pps));
        return m;
    }, [counters]);

    const series = useRollingWindow(ppsSamples, DEFAULT_MAX_LEN, DEFAULT_INTERVAL_MS);

    const rates = useMemo(() => {
        const m = new Map<string, { pps: number; bps: number }>();
        counters.forEach((r, name) => m.set(name, { pps: r.pps, bps: r.bps }));
        return m;
    }, [counters]);

    return { rates, series };
};

/**
 * Poll function counters via tag selection and produce per-function
 * rate and rolling series.
 */
export const useFunctionCounters = (
    _devices: string[],
    _pipelines: string[],
    functions: string[],
    enabled: boolean,
): RatesAndSeries => {
    const functionsKey = useMemo(() => functions.join('|'), [functions]);

    const fetchCounters = useMemo(() => async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const totals = new Map<string, { packets: bigint; bytes: bigint }>();
        for (const f of functions) {
            totals.set(f, { packets: BigInt(0), bytes: BigInt(0) });
        }

        try {
            const response = await API.counters.byTags({
                tags: [
                    { key: 'function', value: '*' },
                    { key: 'chain', value: '' },
                ],
                query: ['input', 'input_bytes'],
            });
            const grouped = groupCounterGroupsByTagsAndName(response.groups, ['function'], 0);
            for (const functionName of functions) {
                totals.set(functionName, {
                    packets: grouped.get(makeGroupedCounterKey([functionName], 'input'))?.value ?? BigInt(0),
                    bytes: grouped.get(makeGroupedCounterKey([functionName], 'input_bytes'))?.value ?? BigInt(0),
                });
            }
        } catch {
            // tolerate fetch failures.
        }

        return totals;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [functionsKey]);

    const { counters } = useInterpolatedCounters({
        keys: functions,
        fetchCounters,
        enabled: enabled && functions.length > 0,
        pollingInterval: DEFAULT_INTERVAL_MS,
    });

    const ppsSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((r, name) => m.set(name, r.pps));
        return m;
    }, [counters]);

    const series = useRollingWindow(ppsSamples, DEFAULT_MAX_LEN, DEFAULT_INTERVAL_MS);

    const rates = useMemo(() => {
        const m = new Map<string, { pps: number; bps: number }>();
        counters.forEach((r, name) => m.set(name, { pps: r.pps, bps: r.bps }));
        return m;
    }, [counters]);

    return { rates, series };
};
