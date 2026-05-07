import { useEffect, useRef, useState, useMemo } from 'react';
import { API } from '../../../api';
import type { CounterInfo, CountersResponse } from '../../../api';
import type { DeviceCounterData } from '../../../hooks';

const DEFAULT_INTERVAL_MS = 1500;
const DEFAULT_MAX_LEN = 30;

const sumCounter = (counters: CounterInfo[] | undefined, name: string): bigint => {
    const c = counters?.find((x) => x.name === name);
    if (!c?.instances) return BigInt(0);
    return c.instances.reduce((sum, inst) => {
        const val = inst.values?.[0];
        return sum + BigInt(val ?? 0);
    }, BigInt(0));
};

/**
 * Aggregate pipeline/function throughput from input/input_bytes counters.
 * These endpoints register input/output/drop counters (not rx/tx); using
 * input represents traffic that entered the pipeline/function regardless
 * of whether it was forwarded or dropped.
 */
const sumPipelineThroughput = (response: CountersResponse): { packets: bigint; bytes: bigint } => {
    const packets = sumCounter(response.counters, 'input');
    const bytes = sumCounter(response.counters, 'input_bytes');
    return { packets, bytes };
};

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
 * Aggregate device pps and produce a rolling throughput series.
 */
export const useThroughputSeries = (
    deviceCounters: Map<string, DeviceCounterData>,
    maxLen: number = DEFAULT_MAX_LEN,
): { current: number; series: number[] } => {
    let current = 0;
    deviceCounters.forEach((d) => {
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
 * Poll pipeline counters across (device, pipeline) pairs and produce
 * per-pipeline rate and rolling series.
 */
export const usePipelineCounters = (
    devices: string[],
    pipelines: string[],
    enabled: boolean,
): RatesAndSeries => {
    const prevRef = useRef<PrevSnapshot | null>(null);
    const [rates, setRates] = useState<Map<string, { pps: number; bps: number }>>(new Map());
    const [series, setSeries] = useState<Map<string, number[]>>(() => new Map());

    const devicesKey = useMemo(() => devices.join('|'), [devices]);
    const pipelinesKey = useMemo(() => pipelines.join('|'), [pipelines]);

    useEffect(() => {
        if (!enabled || devices.length === 0 || pipelines.length === 0) {
            prevRef.current = null;
            setRates(new Map());
            setSeries(new Map());
            return;
        }

        let cancelled = false;

        const tick = async (): Promise<void> => {
            const now = Date.now();
            const totals = new Map<string, { packets: bigint; bytes: bigint }>();
            for (const p of pipelines) {
                totals.set(p, { packets: BigInt(0), bytes: BigInt(0) });
            }

            await Promise.all(
                devices.flatMap((device) =>
                    pipelines.map(async (pipeline) => {
                        try {
                            const resp = await API.counters.pipeline({ device, pipeline });
                            const sums = sumPipelineThroughput(resp);
                            const cur = totals.get(pipeline)!;
                            totals.set(pipeline, {
                                packets: cur.packets + sums.packets,
                                bytes: cur.bytes + sums.bytes,
                            });
                        } catch {
                            // tolerate per-pair failures.
                        }
                    }),
                ),
            );

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
    }, [enabled, devicesKey, pipelinesKey, devices, pipelines]);

    return { rates, series };
};

/**
 * Poll function counters across (device, pipeline, function) triples and
 * produce per-function rate and rolling series.
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

    const devicesKey = useMemo(() => devices.join('|'), [devices]);
    const pipelinesKey = useMemo(() => pipelines.join('|'), [pipelines]);
    const functionsKey = useMemo(() => functions.join('|'), [functions]);

    useEffect(() => {
        if (
            !enabled ||
            devices.length === 0 ||
            pipelines.length === 0 ||
            functions.length === 0
        ) {
            prevRef.current = null;
            setRates(new Map());
            setSeries(new Map());
            return;
        }

        let cancelled = false;

        const tick = async (): Promise<void> => {
            const now = Date.now();
            const totals = new Map<string, { packets: bigint; bytes: bigint }>();
            functions.forEach((f) => totals.set(f, { packets: BigInt(0), bytes: BigInt(0) }));

            const tasks: Promise<void>[] = [];
            for (const device of devices) {
                for (const pipeline of pipelines) {
                    for (const fn of functions) {
                        tasks.push(
                            (async () => {
                                try {
                                    const resp = await API.counters.function({
                                        device,
                                        pipeline,
                                        function: fn,
                                    });
                                    const sums = sumPipelineThroughput(resp);
                                    const cur = totals.get(fn)!;
                                    totals.set(fn, {
                                        packets: cur.packets + sums.packets,
                                        bytes: cur.bytes + sums.bytes,
                                    });
                                } catch {
                                    // tolerate per-triple failures.
                                }
                            })(),
                        );
                    }
                }
            }
            await Promise.all(tasks);

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
    }, [enabled, devicesKey, pipelinesKey, functionsKey, devices, pipelines, functions]);

    return { rates, series };
};
