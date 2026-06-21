import { useModuleRuleCounters } from '@yanet/core/hooks/useModuleRuleCounters';
import type { RuleRate } from '@yanet/core/hooks/useModuleRuleCounters';
import type { RuleItem } from './types';
import { effectiveCounterName } from './hooks';

export type { RuleRate };

export interface UseAclRuleCountersResult {
    /** Map from RuleItem.id to rate data (history + live pps). */
    rates: Map<string, RuleRate>;
}

/**
 * Polls CountersService.ByTags once per second for the enabled subset of ACL rules.
 *
 * Only rules whose counter name appears in enabledCounters are polled; if the set is
 * empty, no requests are made at all. When paused (enabled=false), polling stops but
 * last-known values are preserved so sparklines freeze rather than disappear.
 */
export const useAclRuleCounters = (
    configName: string,
    rules: RuleItem[],
    enabledCounters: Set<string>,
    enabled: boolean,
): UseAclRuleCountersResult => {
    const counterNames = Array.from(enabledCounters).filter(Boolean);

    return useModuleRuleCounters({
        configName,
        rules,
        moduleType: 'acl',
        enabled,
        counterNames,
        getCounterName: (r) => effectiveCounterName(r.rule, r.index),
        isCounterActive: (n) => enabledCounters.has(n),
    });
};
