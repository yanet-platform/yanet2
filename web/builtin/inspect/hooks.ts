import { useMemo } from 'react';
import { API } from '@yanet/core/api';
import type { CounterTag } from '@yanet/core/api/counters';
import type { DeviceCounterData } from '@yanet/core/hooks';
import { useInterpolatedCounters } from '@yanet/core/hooks/useInterpolatedCounters';
import { useRollingWindow } from '@yanet/core/hooks/useRollingWindow';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '@yanet/core/utils';

/**
 * Aggregate RX packet-rate and bit-rate across traffic-source devices.
 *
 * Filters by sourceDeviceNames to avoid double-counting traffic that also
 * appears on stacked virtual devices (e.g. vlan).
 */
export const useAggregateThroughput = (
    rateCounters: Map<string, DeviceCounterData>,
    sourceDeviceNames: Set<string>,
): { aggregatePps: number; aggregateBps: number } => {
    return useMemo(() => {
        let pps = 0;
        let bps = 0;
        rateCounters.forEach((d, name) => {
            if (!sourceDeviceNames.has(name)) return;
            pps += d.rx?.pps ?? 0;
            bps += d.rx?.bps ?? 0;
        });
        return { aggregatePps: pps, aggregateBps: bps };
    }, [rateCounters, sourceDeviceNames]);
};

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
 * Aggregate device pps over traffic-source devices and produce a rolling
 * throughput series. Restricting to source devices avoids double-counting
 * traffic that also appears on stacked virtual devices (e.g. vlan).
 */
export const useThroughputSeries = (
    deviceCounters: Map<string, DeviceCounterData>,
    sourceDeviceNames: Set<string>,
    maxLen: number = DEFAULT_MAX_LEN,
): { current: number; series: number[] } => {
    let current = 0;
    deviceCounters.forEach((d, name) => {
        if (!sourceDeviceNames.has(name)) return;
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

const COUNTER_QUERY = ['input', 'input_bytes'];

/**
 * Poll counters selected by tag filter and produce per-key rate and
 * rolling series, grouped by the given tag keys.
 */
const useTagCounterSeries = (
    keys: string[],
    filterTags: CounterTag[],
    groupTagKeys: string[],
    enabled: boolean,
): RatesAndSeries => {
    const keysKey = useMemo(() => keys.join('|'), [keys]);

    const fetchCounters = useMemo(() => async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const totals = new Map<string, { packets: bigint; bytes: bigint }>();
        for (const k of keys) {
            totals.set(k, { packets: BigInt(0), bytes: BigInt(0) });
        }

        try {
            const response = await API.counters.byTags({
                tags: filterTags,
                query: COUNTER_QUERY,
            });
            const grouped = groupCounterGroupsByTagsAndName(response.groups, groupTagKeys, 0);
            for (const k of keys) {
                totals.set(k, {
                    packets: grouped.get(makeGroupedCounterKey([k], 'input'))?.value ?? BigInt(0),
                    bytes: grouped.get(makeGroupedCounterKey([k], 'input_bytes'))?.value ?? BigInt(0),
                });
            }
        } catch {
            // tolerate fetch failures.
        }

        return totals;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [keysKey]);

    const { counters } = useInterpolatedCounters({
        keys,
        fetchCounters,
        enabled: enabled && keys.length > 0,
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

const PIPELINE_TAGS: CounterTag[] = [
    { key: 'pipeline', value: '*' },
    { key: 'function', value: '' },
];

/**
 * Poll pipeline counters via tag selection and produce per-pipeline
 * rate and rolling series.
 */
export const usePipelineCounters = (
    _devices: string[],
    pipelines: string[],
    enabled: boolean,
): RatesAndSeries => useTagCounterSeries(pipelines, PIPELINE_TAGS, ['pipeline'], enabled);

const FUNCTION_TAGS: CounterTag[] = [
    { key: 'function', value: '*' },
    { key: 'chain', value: '' },
];

/**
 * Poll function counters via tag selection and produce per-function
 * rate and rolling series.
 */
export const useFunctionCounters = (
    _devices: string[],
    _pipelines: string[],
    functions: string[],
    enabled: boolean,
): RatesAndSeries => useTagCounterSeries(functions, FUNCTION_TAGS, ['function'], enabled);
