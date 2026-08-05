import { describe, it, expect } from 'vitest';
import type { FIBEntry, FIBNexthop } from '@yanet/core/api/routes';
import { flattenFIBEntries, rowsToFIBEntries } from './useFIBDraft';
import type { FIBRowItem } from './types';

const nexthop = (device: string, counter = ''): FIBNexthop[] => [{
    dst_mac: { addr: 'aa:bb:cc:dd:ee:ff' },
    src_mac: { addr: '11:22:33:44:55:66' },
    device,
    counter,
}];

const rowFor = (from: string, to: string, overrides: Partial<FIBRowItem> = {}): FIBRowItem => ({
    id: `${from}-${to}`,
    from,
    to,
    dst_mac: 'aa:bb:cc:dd:ee:ff',
    src_mac: '11:22:33:44:55:66',
    device: 'eth0',
    counter: '',
    ...overrides,
});

describe('flattenFIBEntries', () => {
    it('keeps a non-CIDR-aligned range as a single row instead of exploding into CIDRs', () => {
        const entries: FIBEntry[] = [{
            range: { start: '10.0.0.1', end: '10.0.0.130' },
            nexthops: nexthop('eth0'),
        }];

        const rows = flattenFIBEntries(entries);

        expect(rows).toHaveLength(1);
        expect(rows[0].from).toBe('10.0.0.1');
        expect(rows[0].to).toBe('10.0.0.130');
    });

    it('repeats the range on every ECMP nexthop row', () => {
        const entries: FIBEntry[] = [{
            range: { start: '10.0.0.0', end: '10.0.0.255' },
            nexthops: [...nexthop('eth0'), ...nexthop('eth1')],
        }];

        const rows = flattenFIBEntries(entries);

        expect(rows).toHaveLength(2);
        expect(rows.every((r) => r.from === '10.0.0.0' && r.to === '10.0.0.255')).toBe(true);
    });

    it('preserves an explicit counter across a load-then-save round trip', () => {
        const entries: FIBEntry[] = [{
            range: { start: '10.0.0.0', end: '10.0.0.255' },
            nexthops: nexthop('eth0', 'nexthop_custom-counter'),
        }];

        const rows = flattenFIBEntries(entries);
        expect(rows[0].counter).toBe('nexthop_custom-counter');

        const rebuilt = rowsToFIBEntries(rows);
        expect(rebuilt[0].nexthops?.[0].counter).toBe('nexthop_custom-counter');
    });

    it('preserves an empty counter as empty, not undefined', () => {
        const entries: FIBEntry[] = [{
            range: { start: '10.0.0.0', end: '10.0.0.255' },
            nexthops: nexthop('eth0', ''),
        }];

        const rows = flattenFIBEntries(entries);
        expect(rows[0].counter).toBe('');

        const rebuilt = rowsToFIBEntries(rows);
        expect(rebuilt[0].nexthops?.[0].counter).toBe('');
    });
});

describe('rowsToFIBEntries commit ordering', () => {
    it('emits narrower ranges after broader ones so last-write-wins reproduces LPM', () => {
        const rows = [
            rowFor('10.0.0.0', '10.0.0.255'), // /24, narrower
            rowFor('10.0.0.0', '10.255.255.255'), // /8, broader
        ];

        const entries = rowsToFIBEntries(rows);

        expect(entries).toHaveLength(2);
        expect(entries[0].range?.end).toBe('10.255.255.255');
        expect(entries[1].range?.end).toBe('10.0.0.255');
    });

    it('keeps original relative order for ranges of equal span', () => {
        const rows = [
            rowFor('10.0.0.0', '10.0.0.255'),
            rowFor('10.0.1.0', '10.0.1.255'),
        ];

        const entries = rowsToFIBEntries(rows);

        expect(entries[0].range?.start).toBe('10.0.0.0');
        expect(entries[1].range?.start).toBe('10.0.1.0');
    });

    it('throws rather than silently dropping a row with an unparseable range', () => {
        const rows = [rowFor('not-an-ip', '10.0.0.255')];
        expect(() => rowsToFIBEntries(rows)).toThrow();
    });

    it('merges two non-adjacent rows sharing a range into one entry with both nexthops', () => {
        const rows = [
            rowFor('10.0.0.0', '10.0.0.255', { device: 'eth0' }),
            rowFor('10.0.1.0', '10.0.1.255', { device: 'eth1' }),
            rowFor('10.0.0.0', '10.0.0.255', { device: 'eth2' }),
        ];

        const entries = rowsToFIBEntries(rows);

        const merged = entries.find((e) => e.range?.start === '10.0.0.0' && e.range?.end === '10.0.0.255');
        expect(entries).toHaveLength(2);
        expect(merged?.nexthops?.map((nh) => nh.device)).toEqual(['eth0', 'eth2']);
    });
});
