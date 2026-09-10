import { describe, it, expect } from 'vitest';
import { getNeighbourId, getIPWideRemovalRows, resolveSubmitTable, validateMAC, validateNextHop, sortComparators } from './utils';
import { getMergeDebug } from './mergeDebug';
import { MERGED_TAB } from './types';
import type { Neighbour } from '@yanet/core/api/neighbours';

describe('neighbour pair identity', () => {
    it('keeps equal next hops on different devices independently selectable', () => {
        const first = { next_hop: 'fe80::1', device: 'kni0' };
        const second = { next_hop: 'fe80::1', device: 'kni1' };
        expect(getNeighbourId(first)).not.toBe(getNeighbourId(second));
        const selected = new Set([getNeighbourId(second)]);
        expect([first, second].filter((row) => selected.has(getNeighbourId(row)))).toEqual([second]);
    });

    it('canonicalizes mapped IPv4 and equivalent IPv6 spellings', () => {
        expect(getNeighbourId({ next_hop: '::ffff:192.0.2.1', device: 'kni0' }))
            .toBe(getNeighbourId({ next_hop: '192.0.2.1', device: 'kni0' }));
        expect(getNeighbourId({ next_hop: 'FE80:0:0:0:0:0:0:1', device: 'kni0' }))
            .toBe(getNeighbourId({ next_hop: 'fe80::1', device: 'kni0' }));
    });

    it('expands removal to unselected devices sharing a selected IP', () => {
        const selected = { next_hop: '192.0.2.1', device: 'kni0' };
        const sibling = { next_hop: '::ffff:192.0.2.1', device: 'kni1' };
        const unrelated = { next_hop: '192.0.2.2', device: 'kni1' };
        expect(getIPWideRemovalRows([selected, sibling, unrelated], [selected])).toEqual([selected, sibling]);
    });

    it('compares merge candidates only within the winning device scope', () => {
        const winner = { next_hop: '192.0.2.1', device: 'kni0', source: 'static', link_addr: '02:00:00:00:00:01' };
        const samePair = { ...winner, next_hop: '::ffff:192.0.2.1', source: 'remote' };
        const otherDevice = { ...samePair, device: 'kni1', link_addr: '02:00:00:00:00:02' };
        const result = getMergeDebug(winner, new Map([['remote', [otherDevice, samePair]]]), [{ name: 'remote', default_priority: 100 }]);
        expect(result.shadowed.map((candidate) => candidate.entry)).toEqual([samePair]);
        expect(result.macConflict).toBe(false);
    });
});

describe('resolveSubmitTable', () => {
    const makeNeighbour = (source?: string): Neighbour => ({ source });

    it('add + merged tab: returns selectedTable when provided', () => {
        const result = resolveSubmitTable('add', MERGED_TAB, 'arp', 'static', null);
        expect(result).toBe('arp');
    });

    it('add + merged tab: falls back to defaultTable when selectedTable is undefined', () => {
        const result = resolveSubmitTable('add', MERGED_TAB, undefined, 'static', null);
        expect(result).toBe('static');
    });

    it('add + non-merged tab: returns activeTable', () => {
        const result = resolveSubmitTable('add', 'arp', 'static', 'static', null);
        expect(result).toBe('arp');
    });

    it('edit + merged tab: returns neighbour.source', () => {
        const result = resolveSubmitTable('edit', MERGED_TAB, undefined, 'static', makeNeighbour('arp'));
        expect(result).toBe('arp');
    });

    it('edit + merged tab + no neighbour.source: falls back to static', () => {
        const result = resolveSubmitTable('edit', MERGED_TAB, undefined, 'static', makeNeighbour(undefined));
        expect(result).toBe('static');
    });

    it('edit + non-merged tab: returns activeTable', () => {
        const result = resolveSubmitTable('edit', 'ndp', undefined, 'static', makeNeighbour('arp'));
        expect(result).toBe('ndp');
    });
});

describe('validateMAC', () => {
    it('returns undefined for a valid lowercase MAC', () => {
        expect(validateMAC('52:54:00:12:34:56')).toBeUndefined();
    });

    it('returns undefined for an empty string (no options)', () => {
        expect(validateMAC('')).toBeUndefined();
    });

    it('returns undefined for a whitespace-only string (no options)', () => {
        expect(validateMAC('   ')).toBeUndefined();
    });

    it('returns an error message for garbage input', () => {
        expect(validateMAC('not-a-mac')).toBeTruthy();
    });

    it('returns an error message for an incomplete MAC', () => {
        expect(validateMAC('52:54:00:12')).toBeTruthy();
    });

    it('returns undefined for empty string when required is not set', () => {
        expect(validateMAC('', {})).toBeUndefined();
    });

    it('returns required error for empty string when required is true', () => {
        expect(validateMAC('', { required: true })).toBe('MAC address is required');
    });

    it('returns required error for whitespace-only string when required is true', () => {
        expect(validateMAC('   ', { required: true })).toBe('MAC address is required');
    });

    it('returns undefined for valid MAC when required is true', () => {
        expect(validateMAC('52:54:00:12:34:56', { required: true })).toBeUndefined();
    });

    it('returns format error for invalid MAC regardless of required option', () => {
        expect(validateMAC('not-a-mac', { required: true })).toBe('Invalid MAC address (expected xx:xx:xx:xx:xx:xx)');
        expect(validateMAC('not-a-mac', { required: false })).toBe('Invalid MAC address (expected xx:xx:xx:xx:xx:xx)');
    });
});

describe('validateNextHop', () => {
    it('returns undefined for a valid IPv4 address', () => {
        expect(validateNextHop('192.168.1.1')).toBeUndefined();
    });

    it('returns undefined for a valid IPv6 address', () => {
        expect(validateNextHop('fe80::1')).toBeUndefined();
    });

    it('returns an error for an empty string', () => {
        expect(validateNextHop('')).toBeTruthy();
    });

    it('returns an error for garbage input', () => {
        expect(validateNextHop('not-an-ip')).toBeTruthy();
    });
});

describe('sortComparators', () => {
    const makeNeighbour = (overrides: Partial<Neighbour>): Neighbour => ({ ...overrides });

    it('next_hop: sorts by IP string representation', () => {
        const a = makeNeighbour({ next_hop: '10.0.0.1' });
        const b = makeNeighbour({ next_hop: '192.168.1.1' });
        expect(sortComparators.next_hop(a, b)).toBeLessThan(0);
        expect(sortComparators.next_hop(b, a)).toBeGreaterThan(0);
    });

    it('priority: sorts numerically', () => {
        const a = makeNeighbour({ priority: 10 });
        const b = makeNeighbour({ priority: 200 });
        expect(sortComparators.priority(a, b)).toBeLessThan(0);
        expect(sortComparators.priority(b, a)).toBeGreaterThan(0);
    });

    it('priority: treats missing priority as 0', () => {
        const a = makeNeighbour({});
        const b = makeNeighbour({ priority: 5 });
        expect(sortComparators.priority(a, b)).toBeLessThan(0);
    });

    it('source: sorts lexicographically', () => {
        const a = makeNeighbour({ source: 'arp' });
        const b = makeNeighbour({ source: 'static' });
        expect(sortComparators.source(a, b)).toBeLessThan(0);
    });

    it('device: sorts lexicographically', () => {
        const a = makeNeighbour({ device: 'eth0' });
        const b = makeNeighbour({ device: 'eth1' });
        expect(sortComparators.device(a, b)).toBeLessThan(0);
    });
});
