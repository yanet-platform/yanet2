import { useState, useEffect, useCallback, useRef } from 'react';
import { API } from '../../../api';
import type { CounterInfo } from '../../../api';
import { useInterpolatedCounters } from '../../../hooks';
import type { RuleItem } from './types';
import { effectiveCounterName } from './hooks';

const HISTORY_SIZE = 60;

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

interface MountPoint {
    device: string;
    pipeline: string;
    functionName: string;
    chainName: string;
}

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
            const hasAcl = (chain.modules ?? []).some(
                m => m.type === 'acl' && m.name === configName
            );
            if (!hasAcl) continue;

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

/** Per-rule rate data: rolling history for the sparkline and the latest interpolated pps. */
export interface RuleRate {
    history: number[];
    pps: number;
}

export interface UseAclRuleCountersResult {
    /** Map from RuleItem.id to rate data (history + live pps). */
    rates: Map<string, RuleRate>;
}

/**
 * Polls CountersService.Module once per second for the enabled subset of ACL rules.
 * Only rules whose counter name appears in enabledCounters are polled; if the set is
 * empty, no requests are made at all.
 *
 * When paused (enabled=false), polling stops but last-known values are preserved so
 * sparklines freeze rather than disappear.
 */
export const useAclRuleCounters = (
    configName: string,
    rules: RuleItem[],
    enabledCounters: Set<string>,
    enabled: boolean,
): UseAclRuleCountersResult => {
    const [mountPointsEntry, setMountPointsEntry] = useState<{ config: string; points: MountPoint[] }>({ config: '', points: [] });
    const mountPoints = mountPointsEntry.config === configName ? mountPointsEntry.points : [];

    const historyRef = useRef<Map<string, number[]>>(new Map());
    const [rates, setRates] = useState<Map<string, RuleRate>>(new Map());
    const ratesRef = useRef(rates);
    ratesRef.current = rates;

    // Declared here so the effect below (which reads countersRef.current) can reference it safely.
    const countersRef = useRef<Map<string, { pps: number }>>(new Map());

    const discoveryGen = useRef(0);

    useEffect(() => {
        if (!configName) return;
        historyRef.current = new Map();
        setMountPointsEntry({ config: '', points: [] });
        setRates(new Map());
        discoveryGen.current += 1;
        const myGen = discoveryGen.current;
        discoverMountPoints(configName).then(points => {
            if (myGen !== discoveryGen.current) return;
            setMountPointsEntry({ config: configName, points });
        }).catch((err: unknown) => {
            if (myGen !== discoveryGen.current) return;
            console.error('Failed to discover ACL mount points:', err);
        });
    }, [configName]); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
        const history = historyRef.current;
        const next = new Map<string, RuleRate>();
        for (const rule of rules) {
            const cname = effectiveCounterName(rule.rule, rule.index);
            if (!enabledCounters.has(cname)) continue;
            const h = history.get(cname);
            if (h) next.set(rule.id, { history: h, pps: countersRef.current.get(cname)?.pps ?? 0 });
        }
        if (next.size === 0 && ratesRef.current.size === 0) return;
        setRates(next);
    }, [rules, enabledCounters]); // eslint-disable-line react-hooks/exhaustive-deps

    const rulesRef = useRef(rules);
    rulesRef.current = rules;
    const mountPointsRef = useRef(mountPoints);
    mountPointsRef.current = mountPoints;
    const enabledCountersRef = useRef(enabledCounters);
    enabledCountersRef.current = enabledCounters;

    const counterNames = Array.from(enabledCounters).filter(Boolean);

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
                module_type: 'acl',
                module_name: configName,
                counter_query: counterNames,
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
                result.set(counterName, { packets: current.packets + packets, bytes: BigInt(0) });
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

    countersRef.current = counters;

    useEffect(() => {
        if (!enabled || counterNames.length === 0) return;

        const tick = (): void => {
            const currentCounters = countersRef.current;
            const currentRules = rulesRef.current;
            const currentEnabled = enabledCountersRef.current;
            if (!currentCounters.size || !currentRules.length) return;

            const history = historyRef.current;
            let mutated = false;

            for (const [counterName, data] of currentCounters.entries()) {
                if (!currentEnabled.has(counterName)) continue;
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

            const next = new Map<string, RuleRate>();
            for (const rule of currentRules) {
                const cname = effectiveCounterName(rule.rule, rule.index);
                if (!currentEnabled.has(cname)) continue;
                const h = history.get(cname);
                if (h) {
                    const pps = currentCounters.get(cname)?.pps ?? 0;
                    next.set(rule.id, { history: h, pps });
                }
            }
            setRates(next);
        };

        tick();
        const id = setInterval(tick, 1000);
        return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [enabled, counterNames.join(',')]);

    return { rates };
};
