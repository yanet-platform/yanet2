import { useState, useEffect, useCallback, useRef } from 'react';
import { API } from '../../../api';
import { useInterpolatedCounters } from '../../../hooks';
import type { RuleItem } from './types';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../../../utils';

const HISTORY_SIZE = 60;

/** Returns a new array with v appended, capped at cap elements. */
const appendCapped = (arr: number[], v: number, cap: number): number[] =>
    arr.length < cap ? [...arr, v] : [...arr.slice(1), v];

/** Per-rule rate data: rolling history for the sparkline and the latest interpolated pps. */
export interface RuleRate {
    history: number[];
    pps: number;
}

export interface UseForwardRuleCountersResult {
    /** Map from RuleItem.id to rate data (history + live pps). */
    rates: Map<string, RuleRate>;
}

/**
 * Polls CountersService.ByTags once per second for all rules of the given
 * forward config, maintains a 60-sample pps rolling window per rule, and
 * interpolates at ~30ms for smooth sparkline animation.
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
    const historyRef = useRef<Map<string, number[]>>(new Map());

    // Map<ruleId, RuleRate> — returned snapshot; reference changes on each sample.
    const [rates, setRates] = useState<Map<string, RuleRate>>(new Map());
    const ratesRef = useRef(rates);
    ratesRef.current = rates;

    useEffect(() => {
        historyRef.current = new Map();
        setRates(new Map());
    }, [configName]);

    useEffect(() => {
        const history = historyRef.current;
        const next = new Map<string, RuleRate>();
        for (const rule of rules) {
            const h = rule.counter ? history.get(rule.counter) : undefined;
            if (h) {
                next.set(rule.id, { history: h, pps: countersRef.current.get(rule.counter)?.pps ?? 0 });
            }
        }
        if (next.size === 0 && ratesRef.current.size === 0) return;
        setRates(next);
    }, [rules]);

    // Stable refs so callbacks don't need to re-create on every render.
    const rulesRef = useRef(rules);
    rulesRef.current = rules;

    // Unique counter names across all rules — these are the keys for useInterpolatedCounters.
    const counterNames = Array.from(new Set(rules.map(r => r.counter).filter(Boolean)));

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
                    { key: 'module_type', value: 'forward' },
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

            const next = new Map<string, RuleRate>();
            for (const rule of currentRules) {
                const h = rule.counter ? history.get(rule.counter) : undefined;
                if (h) {
                    const pps = rule.counter ? (currentCounters.get(rule.counter)?.pps ?? 0) : 0;
                    next.set(rule.id, { history: h, pps });
                }
            }
            setRates(next);
        };

        tick();
        const id = setInterval(tick, 1000);
        return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [enabled]);

    return { rates };
};
