import type { CounterGroup, CounterInfo } from '../api';

export interface GroupedCounterValue {
    counterName: string;
    tagValues: string[];
    value: bigint;
}

export const getCounterGroupTagValue = (group: CounterGroup, key: string): string | undefined => {
    return group.tags?.find((tag) => tag.key === key)?.value;
};

export const sumCounterInfoValuesAtIndex = (counter: CounterInfo | undefined, index: number): bigint => {
    if (!counter?.instances) {
        return BigInt(0);
    }

    return counter.instances.reduce((sum, instance) => {
        return sum + BigInt(instance.values?.[index] ?? 0);
    }, BigInt(0));
};

export const makeGroupedCounterKey = (tagValues: string[], counterName: string): string => {
    return JSON.stringify([...tagValues, counterName]);
};

export interface GroupedPacketsAndBytes {
    packets: bigint;
    bytes: bigint;
}

/**
 * Group counter groups by tag values + counter name and sum both the
 * packets (values[0]) and bytes (values[1]) dimensions.
 *
 * For size-2 counters where values[0] = packets and values[1] = bytes.
 */
export const groupCounterPacketsAndBytes = (
    groups: CounterGroup[] | undefined,
    tagKeys: string[],
): Map<string, GroupedPacketsAndBytes> => {
    const result = new Map<string, GroupedPacketsAndBytes>();

    for (const group of groups ?? []) {
        const tagValues = tagKeys.map((tagKey) => getCounterGroupTagValue(group, tagKey) ?? '');
        for (const counter of group.counters ?? []) {
            const counterName = counter.name ?? '';
            if (!counterName) {
                continue;
            }

            const key = makeGroupedCounterKey(tagValues, counterName);
            const packets = sumCounterInfoValuesAtIndex(counter, 0);
            const bytes = sumCounterInfoValuesAtIndex(counter, 1);
            const existing = result.get(key);
            if (!existing) {
                result.set(key, { packets, bytes });
            } else {
                result.set(key, {
                    packets: existing.packets + packets,
                    bytes: existing.bytes + bytes,
                });
            }
        }
    }

    return result;
};

export const groupCounterGroupsByTagsAndName = (
    groups: CounterGroup[] | undefined,
    tagKeys: string[],
    valueIndex: number
): Map<string, GroupedCounterValue> => {
    const result = new Map<string, GroupedCounterValue>();

    for (const group of groups ?? []) {
        const tagValues = tagKeys.map((tagKey) => getCounterGroupTagValue(group, tagKey) ?? '');
        for (const counter of group.counters ?? []) {
            const counterName = counter.name ?? '';
            if (!counterName) {
                continue;
            }

            const key = makeGroupedCounterKey(tagValues, counterName);
            const current = result.get(key);
            const increment = sumCounterInfoValuesAtIndex(counter, valueIndex);
            if (!current) {
                result.set(key, { counterName, tagValues, value: increment });
                continue;
            }

            result.set(key, { ...current, value: current.value + increment });
        }
    }

    return result;
};
