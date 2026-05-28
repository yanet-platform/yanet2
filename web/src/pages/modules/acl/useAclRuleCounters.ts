import { useState, useEffect, useCallback, useRef } from 'react';
import { API } from '../../../api';
import { useInterpolatedCounters } from '../../../hooks';
import type { RuleItem } from './types';
import { effectiveCounterName } from './hooks';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../utils';

const HISTORY_SIZE = 60;

const appendCapped = (arr: number[], v: number, cap: number): number[] =>
    arr.length < cap ? [...arr, v] : [...arr.slice(1), v];

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
 * Polls CountersService.ByTags once per second for the enabled subset of ACL rules.
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
    const historyRef = useRef<Map<string, number[]>>(new Map());
    const [rates, setRates] = useState<Map<string, RuleRate>>(new Map());
    const ratesRef = useRef(rates);
    ratesRef.current = rates;

    // Declared here so the effect below (which reads countersRef.current) can reference it safely.
    const countersRef = useRef<Map<string, { pps: number }>>(new Map());

    useEffect(() => {
        historyRef.current = new Map();
        setRates(new Map());
    }, [configName]);

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
    const enabledCountersRef = useRef(enabledCounters);
    enabledCountersRef.current = enabledCounters;

    const counterNames = Array.from(enabledCounters).filter(Boolean);

    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const result = new Map<string, { packets: bigint; bytes: bigint }>();
        for (const name of counterNames) {
            result.set(name, { packets: BigInt(0), bytes: BigInt(0) });
        }

        if (!configName || counterNames.length === 0) {
            return result;
        }

        try {
            const response = await API.counters.byTags({
                tags: [
                    { key: 'module_type', value: 'acl' },
                    { key: 'module_name', value: configName },
                ],
                query: counterNames,
            });
            const grouped = groupCounterGroupsByTagsAndName(response.groups, [], 0);
            for (const counterName of counterNames) {
                result.set(counterName, {
                    packets: grouped.get(makeGroupedCounterKey([], counterName))?.value ?? BigInt(0),
                    bytes: BigInt(0),
                });
            }
        } catch {
            // tolerate fetch failures.
        }

        return result;
    }, [configName, counterNames]);

    const { counters } = useInterpolatedCounters({
        keys: counterNames,
        fetchCounters,
        enabled: enabled && configName.length > 0 && counterNames.length > 0,
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
