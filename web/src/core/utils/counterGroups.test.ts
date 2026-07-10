import { describe, expect, it } from 'vitest';
import {
    getCounterGroupTagValue,
    groupCounterGroupsByTagsAndName,
    groupCounterPacketsAndBytes,
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

describe('groupCounterPacketsAndBytes', () => {
    it('groups size-2 counters into {packets, bytes} per tag+name key', () => {
        const groups: CounterGroup[] = [
            {
                tags: [{ key: 'device', value: 'eth0' }],
                counters: [
                    {
                        name: 'rx',
                        instances: [
                            { values: [10, 1000] },
                            { values: [5, 500] },
                        ],
                    },
                ],
            },
            {
                tags: [{ key: 'device', value: 'eth0' }],
                counters: [
                    {
                        name: 'tx',
                        instances: [{ values: [3, 300] }],
                    },
                ],
            },
            {
                tags: [{ key: 'device', value: 'eth1' }],
                counters: [
                    {
                        name: 'rx',
                        instances: [{ values: [7, 700] }],
                    },
                ],
            },
        ];

        const grouped = groupCounterPacketsAndBytes(groups, ['device']);
        expect(grouped.get(makeGroupedCounterKey(['eth0'], 'rx'))).toEqual({
            packets: BigInt(15),
            bytes: BigInt(1500),
        });
        expect(grouped.get(makeGroupedCounterKey(['eth0'], 'tx'))).toEqual({
            packets: BigInt(3),
            bytes: BigInt(300),
        });
        expect(grouped.get(makeGroupedCounterKey(['eth1'], 'rx'))).toEqual({
            packets: BigInt(7),
            bytes: BigInt(700),
        });
    });
});
