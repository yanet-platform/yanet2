import { describe, expect, it } from 'vitest';
import {
    getCounterGroupTagValue,
    groupCounterGroupsByTagsAndName,
    makeGroupedCounterKey,
    sumCounterInfoValuesAtIndex,
} from './counterGroups';
import type { CounterGroup, CounterInfo } from '../api';

describe('getCounterGroupTagValue', () => {
    it('reads an existing tag value', () => {
        const group: CounterGroup = {
            tags: [{ key: 'device', value: 'eth0' }],
        };

        expect(getCounterGroupTagValue(group, 'device')).toBe('eth0');
        expect(getCounterGroupTagValue(group, 'pipeline')).toBeUndefined();
    });
});

describe('sumCounterInfoValuesAtIndex', () => {
    it('sums values across all instances by index', () => {
        const counter: CounterInfo = {
            name: 'rx',
            instances: [
                { values: [1, 2] },
                { values: [3, 4] },
            ],
        };

        expect(sumCounterInfoValuesAtIndex(counter, 0)).toBe(BigInt(4));
        expect(sumCounterInfoValuesAtIndex(counter, 1)).toBe(BigInt(6));
    });
});

describe('groupCounterGroupsByTagsAndName', () => {
    it('merges duplicate groups and duplicate counter names by selected tags', () => {
        const groups: CounterGroup[] = [
            {
                tags: [{ key: 'pipeline', value: 'p1' }],
                counters: [
                    { name: 'input', instances: [{ values: [10] }] },
                    { name: 'input', instances: [{ values: [5] }] },
                ],
            },
            {
                tags: [{ key: 'pipeline', value: 'p1' }],
                counters: [
                    { name: 'input', instances: [{ values: [7] }] },
                ],
            },
            {
                tags: [{ key: 'pipeline', value: 'p2' }],
                counters: [
                    { name: 'input', instances: [{ values: [100] }] },
                ],
            },
        ];

        const grouped = groupCounterGroupsByTagsAndName(groups, ['pipeline'], 0);
        expect(grouped.get(makeGroupedCounterKey(['p1'], 'input'))?.value).toBe(BigInt(22));
        expect(grouped.get(makeGroupedCounterKey(['p2'], 'input'))?.value).toBe(BigInt(100));
    });
});
