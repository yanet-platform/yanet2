import { useModuleRuleCounters } from '@yanet/core/hooks/useModuleRuleCounters';
import type { RuleRate } from '@yanet/core/hooks/useModuleRuleCounters';
import type { RuleItem } from './types';
import { effectiveCounterName } from './hooks';

export type { RuleRate };

export interface UseForwardRuleCountersResult {
    /** Map from RuleItem.id to rate data (history + live pps). */
    rates: Map<string, RuleRate>;
}

/**
 * Polls CountersService.ByTags once per second for all rules of the given forward config.
 *
 * Maintains a 60-sample pps rolling window per rule and interpolates at ~30 ms for
 * smooth sparkline animation. When enabled=false, polling and history sampling pause.
 * The last known values are preserved so sparklines freeze rather than disappear.
 * Counter names use each rule's effective counter, so rules sharing a default name
 * receive identical sparklines.
 */
export const useForwardRuleCounters = (
    configName: string,
    rules: RuleItem[],
    enabled: boolean,
): UseForwardRuleCountersResult => {
    const counterNames = Array.from(new Set(rules.map(r => effectiveCounterName(r.counter, r.target))));

    return useModuleRuleCounters({
        configName,
        rules,
        moduleType: 'forward',
        enabled,
        counterNames,
        getCounterName: (r) => effectiveCounterName(r.counter, r.target),
    });
};
