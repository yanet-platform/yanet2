import { useModuleRuleCounters } from '../../../hooks/useModuleRuleCounters';
import type { RuleRate } from '../../../hooks/useModuleRuleCounters';
import type { RuleItem } from './types';

export type { RuleRate };

export interface UseForwardRuleCountersResult {
    /** Map from RuleItem.id to rate data (history + live pps). */
    rates: Map<string, RuleRate>;
}

/**
 * Polls CountersService.ByTags once per second for all rules of the given forward config.
 *
 * Maintains a 60-sample pps rolling window per rule and interpolates at ~30 ms for
 * smooth sparkline animation. When enabled=false, polling and history sampling pause;
 * the last known values are preserved so sparklines freeze rather than disappear.
 * Counter names are read from RuleItem.counter; multiple rules sharing the same counter
 * name receive identical sparklines.
 */
export const useForwardRuleCounters = (
    configName: string,
    rules: RuleItem[],
    enabled: boolean,
): UseForwardRuleCountersResult => {
    const counterNames = Array.from(new Set(rules.map(r => r.counter).filter(Boolean)));

    return useModuleRuleCounters({
        configName,
        rules,
        moduleType: 'forward',
        enabled,
        counterNames,
        getCounterName: (r) => r.counter ?? '',
    });
};
