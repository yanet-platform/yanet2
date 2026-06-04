import { useCallback, useMemo } from 'react';
import { API } from '../api';
import { useInterpolatedCounters } from './useInterpolatedCounters';
import { useRollingWindow } from './useRollingWindow';
import { HISTORY_SIZE } from './useCounterHistory';
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
 * interpolates via RAF lag for smooth sparkline animation. Rates are keyed by
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

    const counterNamesKey = counterNames.join(',');

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

    // Build the pps sample map — only include active counters.
    const ppsSamples = useMemo((): Map<string, number> => {
        const m = new Map<string, number>();
        counters.forEach((data, name) => {
            if (isCounterActive && !isCounterActive(name)) return;
            m.set(name, data.pps);
        });
        return m;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [counters, counterNamesKey, isCounterActive]);

    const history = useRollingWindow(ppsSamples, HISTORY_SIZE, 1000, configName);

    // Assemble RuleRate map keyed by rule id.
    const rates = useMemo(() => {
        const next = new Map<string, RuleRate>();
        for (const rule of rules) {
            const cname = getCounterName(rule);
            if (!cname) continue;
            if (isCounterActive && !isCounterActive(cname)) continue;
            const h = history.get(cname);
            if (!h) continue;
            const pps = counters.get(cname)?.pps ?? 0;
            next.set(rule.id, { history: h, pps });
        }
        return next;
    }, [rules, history, counters, getCounterName, isCounterActive]);

    return { rates };
};
