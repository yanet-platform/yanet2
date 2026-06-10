import { useMemo } from 'react';
import type { InterpolatedCounterData } from './useInterpolatedCounters';

/**
 * Copies a counters map into a new Map for stable identity and memoized lookup.
 */
export const useInterpolatedCounterMap = (
    counters: Map<string, InterpolatedCounterData>,
): Map<string, InterpolatedCounterData> =>
    useMemo(() => {
        const map = new Map<string, InterpolatedCounterData>();
        for (const [key, val] of counters.entries()) {
            map.set(key, val);
        }
        return map;
    }, [counters]);
