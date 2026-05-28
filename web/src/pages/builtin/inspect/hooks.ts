import { useEffect, useRef, useState, useMemo } from 'react';
import { API } from '../../../api';
import type { DeviceCounterData } from '../../../hooks';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../utils';

const DEFAULT_INTERVAL_MS = 1500;
const DEFAULT_MAX_LEN = 30;

/**
 * Push the current value onto a rolling history at the polling interval.
 */
export const useRollingSeries = (
    value: number | undefined,
    maxLen: number = DEFAULT_MAX_LEN,
    intervalMs: number = DEFAULT_INTERVAL_MS,
): number[] => {
    const valueRef = useRef<number>(value ?? 0);
    valueRef.current = value ?? 0;

    const [series, setSeries] = useState<number[]>([]);

    useEffect(() => {
        const id = setInterval(() => {
            setSeries((prev) => {
                if (prev.length === 0 && valueRef.current === 0) {
                    return prev;
                }
                const next = [...prev, valueRef.current];
                if (next.length > maxLen) next.shift();
                return next;
            });
        }, intervalMs);
        return () => clearInterval(id);
    }, [maxLen, intervalMs]);

    return series;
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
    const deviceCountersRef = useRef(deviceCounters);
    deviceCountersRef.current = deviceCounters;
    const kindRef = useRef(kind);
    kindRef.current = kind;

    const [series, setSeries] = useState<Map<string, number[]>>(() => new Map());

    useEffect(() => {
        const id = setInterval(() => {
            const counters = deviceCountersRef.current;
            const k = kindRef.current;
            setSeries((prev) => {
                const next = new Map<string, number[]>();
                counters.forEach((d, name) => {
                    const v = (k === 'rx' ? d.rx?.pps : d.tx?.pps) ?? 0;
                    const old = prev.get(name);
                    if (old === undefined && v === 0) {
                        return;
                    }
                    const grown = [...(old ?? []), v];
                    if (grown.length > maxLen) grown.shift();
                    next.set(name, grown);
                });
                return next;
            });
        }, DEFAULT_INTERVAL_MS);
        return () => clearInterval(id);
    }, [maxLen]);

    return series;
};

interface PrevSnapshot {
    timestamp: number;
    values: Map<string, { packets: bigint; bytes: bigint }>;
}

interface RatesAndSeries {
    rates: Map<string, { pps: number; bps: number }>;
    series: Map<string, number[]>;
}

/**
 * Poll pipeline counters via tag selection and produce per-pipeline
 * rate and rolling series.
 */
export const usePipelineCounters = (
    devices: string[],
    pipelines: string[],
    enabled: boolean,
): RatesAndSeries => {
    const prevRef = useRef<PrevSnapshot | null>(null);
    const [rates, setRates] = useState<Map<string, { pps: number; bps: number }>>(new Map());
    const [series, setSeries] = useState<Map<string, number[]>>(() => new Map());

    const devicesRef = useRef(devices);
    devicesRef.current = devices;
    const pipelinesRef = useRef(pipelines);
    pipelinesRef.current = pipelines;

    const devicesKey = useMemo(() => devices.join('|'), [devices]);
    const pipelinesKey = useMemo(() => pipelines.join('|'), [pipelines]);

    useEffect(() => {
        if (!enabled || devicesRef.current.length === 0 || pipelinesRef.current.length === 0) {
            prevRef.current = null;
            setRates(new Map());
            setSeries(new Map());
            return;
        }

        let cancelled = false;

        const tick = async (): Promise<void> => {
            const now = Date.now();
            const currentPipelines = pipelinesRef.current;

            const totals = new Map<string, { packets: bigint; bytes: bigint }>();
            for (const p of currentPipelines) {
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
                for (const pipeline of currentPipelines) {
                    totals.set(pipeline, {
                        packets: grouped.get(makeGroupedCounterKey([pipeline], 'input'))?.value ?? BigInt(0),
                        bytes: grouped.get(makeGroupedCounterKey([pipeline], 'input_bytes'))?.value ?? BigInt(0),
                    });
                }
            } catch {
                // tolerate fetch failures.
            }

            if (cancelled) return;

            const newRates = new Map<string, { pps: number; bps: number }>();
            const prev = prevRef.current;
            if (prev) {
                const dt = (now - prev.timestamp) / 1000;
                if (dt > 0) {
                    totals.forEach((cur, name) => {
                        const old = prev.values.get(name) ?? { packets: BigInt(0), bytes: BigInt(0) };
                        const dp = Number(cur.packets - old.packets);
                        const db = Number(cur.bytes - old.bytes);
                        newRates.set(name, {
                            pps: Math.max(0, dp / dt),
                            bps: Math.max(0, db / dt),
                        });
                    });
                }
            }

            const hadPrev = prevRef.current !== null;
            prevRef.current = { timestamp: now, values: totals };

            if (hadPrev) {
                setSeries((prev) => {
                    const next = new Map<string, number[]>();
                    newRates.forEach((r, name) => {
                        const old = prev.get(name) ?? [];
                        const grown = [...old, r.pps];
                        if (grown.length > DEFAULT_MAX_LEN) grown.shift();
                        next.set(name, grown);
                    });
                    return next;
                });
            }

            setRates(newRates);
        };

        tick();
        const id = setInterval(tick, DEFAULT_INTERVAL_MS);
        return () => {
            cancelled = true;
            clearInterval(id);
        };
    }, [enabled, devicesKey, pipelinesKey]);

    return { rates, series };
};

/**
 * Poll function counters via tag selection and produce per-function
 * rate and rolling series.
 */
export const useFunctionCounters = (
    devices: string[],
    pipelines: string[],
    functions: string[],
    enabled: boolean,
): RatesAndSeries => {
    const prevRef = useRef<PrevSnapshot | null>(null);
    const [rates, setRates] = useState<Map<string, { pps: number; bps: number }>>(new Map());
    const [series, setSeries] = useState<Map<string, number[]>>(() => new Map());

    const devicesRef = useRef(devices);
    devicesRef.current = devices;
    const pipelinesRef = useRef(pipelines);
    pipelinesRef.current = pipelines;
    const functionsRef = useRef(functions);
    functionsRef.current = functions;

    const devicesKey = useMemo(() => devices.join('|'), [devices]);
    const pipelinesKey = useMemo(() => pipelines.join('|'), [pipelines]);
    const functionsKey = useMemo(() => functions.join('|'), [functions]);

    useEffect(() => {
        if (
            !enabled ||
            devicesRef.current.length === 0 ||
            pipelinesRef.current.length === 0 ||
            functionsRef.current.length === 0
        ) {
            prevRef.current = null;
            setRates(new Map());
            setSeries(new Map());
            return;
        }

        let cancelled = false;

        const tick = async (): Promise<void> => {
            const now = Date.now();
            const currentFunctions = functionsRef.current;

            const totals = new Map<string, { packets: bigint; bytes: bigint }>();
            currentFunctions.forEach((f) => totals.set(f, { packets: BigInt(0), bytes: BigInt(0) }));

            try {
                const response = await API.counters.byTags({
                    tags: [
                        { key: 'function', value: '*' },
                        { key: 'chain', value: '' },
                    ],
                    query: ['input', 'input_bytes'],
                });
                const grouped = groupCounterGroupsByTagsAndName(response.groups, ['function'], 0);
                for (const functionName of currentFunctions) {
                    totals.set(functionName, {
                        packets: grouped.get(makeGroupedCounterKey([functionName], 'input'))?.value ?? BigInt(0),
                        bytes: grouped.get(makeGroupedCounterKey([functionName], 'input_bytes'))?.value ?? BigInt(0),
                    });
                }
            } catch {
                // tolerate fetch failures.
            }

            if (cancelled) return;

            const newRates = new Map<string, { pps: number; bps: number }>();
            const prev = prevRef.current;
            if (prev) {
                const dt = (now - prev.timestamp) / 1000;
                if (dt > 0) {
                    totals.forEach((cur, name) => {
                        const old = prev.values.get(name) ?? { packets: BigInt(0), bytes: BigInt(0) };
                        const dp = Number(cur.packets - old.packets);
                        const db = Number(cur.bytes - old.bytes);
                        newRates.set(name, {
                            pps: Math.max(0, dp / dt),
                            bps: Math.max(0, db / dt),
                        });
                    });
                }
            }

            const hadPrev = prevRef.current !== null;
            prevRef.current = { timestamp: now, values: totals };

            if (hadPrev) {
                setSeries((prev) => {
                    const next = new Map<string, number[]>();
                    newRates.forEach((r, name) => {
                        const old = prev.get(name) ?? [];
                        const grown = [...old, r.pps];
                        if (grown.length > DEFAULT_MAX_LEN) grown.shift();
                        next.set(name, grown);
                    });
                    return next;
                });
            }

            setRates(newRates);
        };

        tick();
        const id = setInterval(tick, DEFAULT_INTERVAL_MS);
        return () => {
            cancelled = true;
            clearInterval(id);
        };
    }, [enabled, devicesKey, pipelinesKey, functionsKey]);

    return { rates, series };
};

/**
 * Animate a numeric value by lagging one sample behind real time and linearly
 * interpolating from the previous committed sample toward the current one as
 * wall clock advances. Matches the "buffer 2 seconds, draw 0..1 with interp"
 * pattern: the returned value always sits inside [previous, current], never
 * extrapolating past current.
 *
 * The hook commits a new sample only when `value` changes — so the source
 * upstream MUST tick on each poll (which it does for pipeline/function rates).
 * `intervalMs` is the expected cadence between source updates; it sets the
 * animation duration of the trailing segment.
 */
export const useLaggedValue = (value: number, intervalMs: number = 1500): number => {
    const prevRef = useRef<{ value: number; ts: number }>({ value, ts: performance.now() });
    const curRef = useRef<{ value: number; ts: number }>({ value, ts: performance.now() });
    const lastInputRef = useRef<number>(value);
    const [animated, setAnimated] = useState<number>(value);

    if (lastInputRef.current !== value) {
        prevRef.current = curRef.current;
        curRef.current = { value, ts: performance.now() };
        lastInputRef.current = value;
    }

    useEffect(() => {
        let raf = 0;
        const tick = (): void => {
            const now = performance.now();
            const dt = now - curRef.current.ts;
            const t = Math.max(0, Math.min(1, dt / intervalMs));
            const next = prevRef.current.value + (curRef.current.value - prevRef.current.value) * t;
            setAnimated(next);
            raf = requestAnimationFrame(tick);
        };
        raf = requestAnimationFrame(tick);
        return () => cancelAnimationFrame(raf);
    }, [intervalMs]);

    return animated;
};

/**
 * Per-key lag-interpolated rolling series: takes a Map<string, number> of
 * latest sample values (one per key) and returns a Map<string, number[]> where
 * each series is built with lag-interpolation.
 */
export const useLaggedSeriesMap = (
    values: Map<string, number>,
    maxLen: number = DEFAULT_MAX_LEN,
    intervalMs: number = DEFAULT_INTERVAL_MS,
): Map<string, number[]> => {
    const samplesMapRef = useRef<Map<string, number[]>>(new Map());
    const prevMapRef = useRef<Map<string, { value: number; ts: number }>>(new Map());
    const curMapRef = useRef<Map<string, { value: number; ts: number }>>(new Map());
    const lastInputsRef = useRef<Map<string, number>>(new Map());
    const [, force] = useState(0);

    values.forEach((v, k) => {
        const last = lastInputsRef.current.get(k);
        if (last !== v) {
            const now = performance.now();
            const cur = curMapRef.current.get(k);
            const samples = samplesMapRef.current.get(k) ?? [];
            if (cur !== undefined) {
                const next = [...samples, cur.value];
                if (next.length > Math.max(1, maxLen - 1)) {
                    next.shift();
                }
                samplesMapRef.current.set(k, next);
            } else if (samples.length === 0 && v === 0) {
                lastInputsRef.current.set(k, v);
                return;
            }
            prevMapRef.current.set(k, cur ?? { value: v, ts: now });
            curMapRef.current.set(k, { value: v, ts: now });
            lastInputsRef.current.set(k, v);
        }
    });
    [...samplesMapRef.current.keys()].forEach((k) => {
        if (!values.has(k)) {
            samplesMapRef.current.delete(k);
            prevMapRef.current.delete(k);
            curMapRef.current.delete(k);
            lastInputsRef.current.delete(k);
        }
    });

    useEffect(() => {
        let raf = 0;
        const tick = (): void => {
            force((n) => (n + 1) | 0);
            raf = requestAnimationFrame(tick);
        };
        raf = requestAnimationFrame(tick);
        return () => cancelAnimationFrame(raf);
    }, []);

    const out = new Map<string, number[]>();
    const now = performance.now();
    values.forEach((_, k) => {
        const cur = curMapRef.current.get(k);
        const prev = prevMapRef.current.get(k);
        const samples = samplesMapRef.current.get(k) ?? [];
        if (cur === undefined) {
            if (samples.length > 0) out.set(k, samples);
            return;
        }
        const dt = now - cur.ts;
        const t = Math.max(0, Math.min(1, dt / intervalMs));
        const prevVal = prev?.value ?? cur.value;
        const interp = prevVal + (cur.value - prevVal) * t;
        out.set(k, [...samples, interp]);
    });
    return out;
};
