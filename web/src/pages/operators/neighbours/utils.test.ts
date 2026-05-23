import { describe, it, expect } from 'vitest';
import { resolveSubmitTable, validateMAC, validateNextHop, sortComparators } from './utils';
import { MERGED_TAB } from './types';
import type { Neighbour } from '../../../api/neighbours';

// ---------------------------------------------------------------------------
// resolveSubmitTable
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// validateMAC
// ---------------------------------------------------------------------------

describe('validateMAC', () => {
    it('returns undefined for a valid lowercase MAC', () => {
        expect(validateMAC('52:54:00:12:34:56')).toBeUndefined();
    });

    it('returns undefined for an empty string', () => {
        expect(validateMAC('')).toBeUndefined();
    });

    it('returns undefined for a whitespace-only string', () => {
        expect(validateMAC('   ')).toBeUndefined();
    });

    it('returns an error message for garbage input', () => {
        expect(validateMAC('not-a-mac')).toBeTruthy();
    });

    it('returns an error message for an incomplete MAC', () => {
        expect(validateMAC('52:54:00:12')).toBeTruthy();
    });
});

// ---------------------------------------------------------------------------
// validateNextHop
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// sortComparators
// ---------------------------------------------------------------------------

describe('sortComparators', () => {
    const makeNeighbour = (overrides: Partial<Neighbour>): Neighbour => ({ ...overrides });

    it('next_hop: sorts by IP string representation', () => {
        const a = makeNeighbour({ next_hop: { addr: '10.0.0.1' } });
        const b = makeNeighbour({ next_hop: { addr: '192.168.1.1' } });
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
