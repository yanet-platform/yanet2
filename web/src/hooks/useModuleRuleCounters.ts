import { useState, useEffect, useCallback, useRef } from 'react';
import { API } from '../api';
import { useInterpolatedCounters } from './useInterpolatedCounters';
import { appendCapped, HISTORY_SIZE } from './useCounterHistory';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../utils';

/** Per-rule rate data: rolling history for the sparkline and the latest interpolated pps. */
export interface RuleRate {
    history: number[];
    pps: number;
}

/** Parameters for the generic module rule counters hook. */
export interface ModuleRuleCountersParams<T> {
    configName: string;
    rules: T[];
    moduleType: string;
    enabled: boolean;
    /** Counter names to poll; caller derives this list. */
    counterNames: string[];
    /** Returns the counter name for a given rule; empty string means the rule has no counter. */
    getCounterName: (rule: T) => string;
    /** When provided, only counters passing this predicate are sampled and included in rates. */
    isCounterActive?: (name: string) => boolean;
}

/**
 * Generic polling skeleton shared by ACL and forward rule-counter hooks.
 *
 * Polls CountersService.ByTags once per second for the supplied counterNames,
 * maintains a HISTORY_SIZE-sample pps rolling window per counter, and
 * interpolates at ~30 ms for smooth sparkline animation. Rates are keyed by
 * rule id (from the T constraint `{ id: string }`).
 */
export const useModuleRuleCounters = <T extends { id: string }>(
    params: ModuleRuleCountersParams<T>,
): { rates: Map<string, RuleRate> } => {
    const {
        configName,
        rules,
        moduleType,
        enabled,
        counterNames,
        getCounterName,
        isCounterActive,
    } = params;

    const historyRef = useRef<Map<string, number[]>>(new Map());
    const [rates, setRates] = useState<Map<string, RuleRate>>(new Map());
    const ratesRef = useRef(rates);
    ratesRef.current = rates;

    const countersRef = useRef<Map<string, { pps: number }>>(new Map());

    useEffect(() => {
        historyRef.current = new Map();
        setRates(new Map());
    }, [configName]);

    const counterNamesKey = counterNames.join(',');

    useEffect(() => {
        const history = historyRef.current;
        const next = new Map<string, RuleRate>();
        for (const rule of rules) {
            const cname = getCounterName(rule);
            if (!cname) continue;
            if (isCounterActive && !isCounterActive(cname)) continue;
            const h = history.get(cname);
            if (h) next.set(rule.id, { history: h, pps: countersRef.current.get(cname)?.pps ?? 0 });
        }
        if (next.size === 0 && ratesRef.current.size === 0) return;
        setRates(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [rules, counterNamesKey]);

    const rulesRef = useRef(rules);
    rulesRef.current = rules;
    const getCounterNameRef = useRef(getCounterName);
    getCounterNameRef.current = getCounterName;
    const isCounterActiveRef = useRef(isCounterActive);
    isCounterActiveRef.current = isCounterActive;
    const counterNamesRef = useRef(counterNames);
    counterNamesRef.current = counterNames;

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
                    { key: 'module_type', value: moduleType },
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
    }, [configName, moduleType, counterNames]); // eslint-disable-line react-hooks/exhaustive-deps

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
            const currentIsActive = isCounterActiveRef.current;
            if (!currentCounters.size || !currentRules.length) return;

            const history = historyRef.current;
            let mutated = false;

            for (const [counterName, data] of currentCounters.entries()) {
                if (currentIsActive && !currentIsActive(counterName)) continue;
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

            const currentGetName = getCounterNameRef.current;
            const next = new Map<string, RuleRate>();
            for (const rule of currentRules) {
                const cname = currentGetName(rule);
                if (!cname) continue;
                if (currentIsActive && !currentIsActive(cname)) continue;
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
    }, [enabled, counterNamesKey]);

    return { rates };
};
