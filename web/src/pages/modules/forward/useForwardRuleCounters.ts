import { useState, useEffect, useCallback, useRef } from 'react';
import { API } from '../../../api';
import type { CounterInfo } from '../../../api';
import { useInterpolatedCounters } from '../../../hooks';
import type { RuleItem } from './types';

const HISTORY_SIZE = 60;

/** Returns a new array with v appended, capped at cap elements. */
const appendCapped = (arr: number[], v: number, cap: number): number[] =>
    arr.length < cap ? [...arr, v] : [...arr.slice(1), v];

const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) return BigInt(0);
    return counter.instances.reduce((sum, inst) => {
        if (!inst.values) return sum;
        const val = inst.values[0];
        return sum + BigInt(val ?? 0);
    }, BigInt(0));
};

const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined =>
    counters?.find(c => c.name === name);

/** A (device, pipeline, function, chain) quad where the forward module is mounted. */
interface MountPoint {
    device: string;
    pipeline: string;
    functionName: string;
    chainName: string;
}

/**
 * Discovers all mount points for a named forward config by calling InspectService.
 * Returns only the quads where at least one chain module matches
 * { type: 'forward', name: configName }.
 */
const discoverMountPoints = async (configName: string): Promise<MountPoint[]> => {
    const response = await API.inspect.inspect();
    const info = response.instance_info;
    if (!info) return [];

    const pipelines = info.pipelines ?? [];
    const functions = info.functions ?? [];
    const devices = info.devices ?? [];

    const points: MountPoint[] = [];

    for (const fn of functions) {
        const fnName = fn.name ?? '';
        for (const chain of fn.chains ?? []) {
            const chainName = chain.name ?? '';
            const hasForward = (chain.modules ?? []).some(
                m => m.type === 'forward' && m.name === configName
            );
            if (!hasForward) continue;

            for (const pipeline of pipelines) {
                const pipelineName = pipeline.name ?? '';
                if (!(pipeline.functions ?? []).includes(fnName)) continue;

                for (const device of devices) {
                    const deviceName = device.name ?? '';
                    const allDevicePipelines = [
                        ...(device.input_pipelines ?? []),
                        ...(device.output_pipelines ?? []),
                    ];
                    if (!allDevicePipelines.some(dp => dp.name === pipelineName)) continue;

                    points.push({ device: deviceName, pipeline: pipelineName, functionName: fnName, chainName });
                }
            }
        }
    }

    return points;
};

export interface UseForwardRuleCountersResult {
    /** Map from RuleItem.id to pps history (60 samples). Null means not yet seeded. */
    sparklines: Map<string, number[]>;
}

/**
 * Polls CountersService.Module once per second for all rules of the given forward
 * config, maintains a 60-sample pps rolling window per rule, and interpolates at
 * ~30ms for smooth sparkline animation.
 *
 * When enabled=false, polling and history sampling pause; the last known values are
 * preserved so sparklines freeze rather than disappear.
 *
 * Counter names are read from RuleItem.counter. Multiple rules sharing the same
 * counter name receive identical sparklines. The key for history is RuleItem.id.
 */
export const useForwardRuleCounters = (
    configName: string,
    rules: RuleItem[],
    enabled: boolean,
): UseForwardRuleCountersResult => {
    // Mount points are stored together with the config name they were discovered
    // for. Deriving the effective mount points by comparing tags during render
    // ensures that stale points from config A are never visible in a render that
    // already shows config B, even before the discovery effect has had a chance
    // to call setMountPointsEntry([]) after the tab switch.
    const [mountPointsEntry, setMountPointsEntry] = useState<{ config: string; points: MountPoint[] }>({ config: '', points: [] });
    const mountPoints = mountPointsEntry.config === configName ? mountPointsEntry.points : [];

    // Rolling history: Map<counterName, number[]>
    const historyRef = useRef<Map<string, number[]>>(new Map());

    // Map<ruleId, number[]> — returned snapshot; reference changes on each sample.
    const [sparklines, setSparklines] = useState<Map<string, number[]>>(new Map());

    // Monotonically incremented on each configName change. Promise resolutions
    // that carry a stale generation are silently dropped so that a slow request
    // for config A cannot overwrite the state that belongs to config B.
    const discoveryGen = useRef(0);

    useEffect(() => {
        if (!configName) return;
        // Synchronously clear stale state from the previous config so the UI
        // never displays data belonging to a different config instance.
        historyRef.current = new Map();
        setMountPointsEntry({ config: '', points: [] });
        setSparklines(new Map());
        discoveryGen.current += 1;
        const myGen = discoveryGen.current;
        discoverMountPoints(configName).then(points => {
            if (myGen !== discoveryGen.current) return;
            setMountPointsEntry({ config: configName, points });
        }).catch((err: unknown) => {
            if (myGen !== discoveryGen.current) return;
            console.error('Failed to discover forward mount points:', err);
        });
    }, [configName]); // eslint-disable-line react-hooks/exhaustive-deps

    // When rules change (e.g. immediately after a config switch when history has
    // been cleared), rebuild the snapshot so the UI reflects the current history
    // state without waiting for the next 1-second tick.
    useEffect(() => {
        const history = historyRef.current;
        const next = new Map<string, number[]>();
        for (const rule of rules) {
            const h = rule.counter ? history.get(rule.counter) : undefined;
            if (h) next.set(rule.id, h);
        }
        setSparklines(next);
    }, [rules]);

    // Stable refs so callbacks don't need to re-create on every render.
    const rulesRef = useRef(rules);
    rulesRef.current = rules;
    const mountPointsRef = useRef(mountPoints);
    mountPointsRef.current = mountPoints;

    // Unique counter names across all rules — these are the keys for useInterpolatedCounters.
    const counterNames = Array.from(new Set(rules.map(r => r.counter).filter(Boolean)));

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const result = new Map<string, { packets: bigint; bytes: bigint }>();
        for (const name of counterNames) {
            result.set(name, { packets: BigInt(0), bytes: BigInt(0) });
        }

        const points = mountPointsRef.current;
        if (points.length === 0 || counterNames.length === 0) return result;

        const fetches = points.map(mp =>
            API.counters.module({
                device: mp.device,
                pipeline: mp.pipeline,
                function: mp.functionName,
                chain: mp.chainName,
                module_type: 'forward',
                module_name: configName,
                counter_query: [],
            }).then(response => response.counters ?? [])
        );

        const settled = await Promise.allSettled(fetches);

        for (const outcome of settled) {
            if (outcome.status !== 'fulfilled') continue;
            const countersArr = outcome.value;
            for (const counterName of counterNames) {
                const ci = findCounter(countersArr, counterName);
                const packets = sumCounterValues(ci);
                const current = result.get(counterName)!;
                result.set(counterName, { packets: current.packets + packets, bytes: current.bytes });
            }
        }

        return result;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [configName, mountPoints, counterNames.join(',')]);

    const { counters } = useInterpolatedCounters({
        keys: counterNames,
        fetchCounters,
        enabled: enabled && mountPoints.length > 0 && counterNames.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    const countersRef = useRef(counters);
    countersRef.current = counters;

    useEffect(() => {
        if (!enabled) return;

        const tick = (): void => {
            const currentCounters = countersRef.current;
            const currentRules = rulesRef.current;
            if (!currentCounters.size || !currentRules.length) return;

            const history = historyRef.current;
            let mutated = false;

            for (const [counterName, data] of currentCounters.entries()) {
                const pps = data.pps;
                const existing = history.get(counterName);
                if (!existing) {
                    history.set(counterName, Array(HISTORY_SIZE).fill(pps) as number[]);
                } else {
                    history.set(counterName, appendCapped(existing, pps, HISTORY_SIZE));
                }
                mutated = true;
            }

            if (!mutated) return;

            const next = new Map<string, number[]>();
            for (const rule of currentRules) {
                const h = rule.counter ? history.get(rule.counter) : undefined;
                if (h) next.set(rule.id, h);
            }
            setSparklines(next);
        };

        tick();
        const id = setInterval(tick, 1000);
        return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [enabled]);

    return { sparklines };
};
