import { describe, it, expect } from 'vitest';
import { planRouteSubmit, validatePrefix, validateNexthop, sortComparators } from './utils';
import { stringToIPAddress } from '../../../utils/netip';
import type { Route } from '../../../api/routes';

// ---------------------------------------------------------------------------
// planRouteSubmit
// ---------------------------------------------------------------------------

describe('planRouteSubmit', () => {
    const ip = (s: string) => stringToIPAddress(s)!;

    it('add mode: produces a single insert op', () => {
        const ops = planRouteSubmit(
            'add',
            { prefix: '10.0.0.0/8', nexthopIp: ip('192.168.1.1'), doFlush: false },
            '192.168.1.1',
            null,
            '',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
        if (ops[0].type === 'insert') {
            expect(ops[0].prefix).toBe('10.0.0.0/8');
            expect(ops[0].doFlush).toBe(false);
        }
    });

    it('edit mode, no key change: produces a single insert op without delete', () => {
        const original: Route = { prefix: '10.0.0.0/8', next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '10.0.0.0/8', nexthopIp: ip('192.168.1.1'), doFlush: false },
            '192.168.1.1',
            original,
            '192.168.1.1',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
    });

    it('edit mode, prefix changed: produces delete then insert', () => {
        const original: Route = { prefix: '10.0.0.0/8', next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '172.16.0.0/12', nexthopIp: ip('192.168.1.1'), doFlush: false },
            '192.168.1.1',
            original,
            '192.168.1.1',
        );
        expect(ops).toHaveLength(2);
        expect(ops[0].type).toBe('delete');
        if (ops[0].type === 'delete') {
            expect(ops[0].prefix).toBe('10.0.0.0/8');
        }
        expect(ops[1].type).toBe('insert');
        if (ops[1].type === 'insert') {
            expect(ops[1].prefix).toBe('172.16.0.0/12');
        }
    });

    it('edit mode, nexthop changed: produces delete then insert', () => {
        const original: Route = { prefix: '10.0.0.0/8', next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '10.0.0.0/8', nexthopIp: ip('10.0.0.1'), doFlush: true },
            '10.0.0.1',
            original,
            '192.168.1.1',
        );
        expect(ops).toHaveLength(2);
        expect(ops[0].type).toBe('delete');
        expect(ops[1].type).toBe('insert');
        if (ops[1].type === 'insert') {
            expect(ops[1].doFlush).toBe(true);
        }
    });

    it('edit mode, both prefix and nexthop changed: produces delete then insert', () => {
        const original: Route = { prefix: '10.0.0.0/8', next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '172.16.0.0/12', nexthopIp: ip('10.0.0.1'), doFlush: false },
            '10.0.0.1',
            original,
            '192.168.1.1',
        );
        expect(ops).toHaveLength(2);
        expect(ops[0].type).toBe('delete');
        expect(ops[1].type).toBe('insert');
    });

    it('edit mode, original has no prefix: skips delete, produces only insert', () => {
        const original: Route = { next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '10.0.0.0/8', nexthopIp: ip('192.168.1.1'), doFlush: false },
            '192.168.1.1',
            original,
            '192.168.1.1',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
    });

    it('edit mode, original has no next_hop: skips delete, produces only insert', () => {
        const original: Route = { prefix: '10.0.0.0/8' };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '172.16.0.0/12', nexthopIp: ip('192.168.1.1'), doFlush: false },
            '192.168.1.1',
            original,
            '',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
    });
});

// ---------------------------------------------------------------------------
// validatePrefix
// ---------------------------------------------------------------------------

describe('validatePrefix', () => {
    it('returns undefined for a valid IPv4 CIDR', () => {
        expect(validatePrefix('192.168.1.0/24')).toBeUndefined();
    });

    it('returns undefined for a valid IPv6 CIDR', () => {
        expect(validatePrefix('2001:db8::/32')).toBeUndefined();
    });

    it('returns undefined for an empty string (field not yet filled)', () => {
        expect(validatePrefix('')).toBeUndefined();
    });

    it('returns an error message for garbage input', () => {
        expect(validatePrefix('not-a-cidr')).toBeTruthy();
    });

    it('returns an error message for an IP without prefix length', () => {
        expect(validatePrefix('192.168.1.1')).toBeTruthy();
    });
});

// ---------------------------------------------------------------------------
// validateNexthop
// ---------------------------------------------------------------------------

describe('validateNexthop', () => {
    it('returns undefined for a valid IPv4 address', () => {
        expect(validateNexthop('192.168.1.1')).toBeUndefined();
    });

    it('returns undefined for a valid IPv6 address', () => {
        expect(validateNexthop('2001:db8::1')).toBeUndefined();
    });

    it('returns undefined for an empty string', () => {
        expect(validateNexthop('')).toBeUndefined();
    });

    it('returns an error message for garbage input', () => {
        expect(validateNexthop('not-an-ip')).toBeTruthy();
    });
});

// ---------------------------------------------------------------------------
// sortComparators
// ---------------------------------------------------------------------------

describe('sortComparators', () => {
    const makeRoute = (overrides: Partial<Route>): Route => ({ ...overrides });

    it('prefix: sorts lexicographically by prefix string', () => {
        const a = makeRoute({ prefix: '10.0.0.0/8' });
        const b = makeRoute({ prefix: '192.168.0.0/16' });
        expect(sortComparators.prefix(a, b)).toBeLessThan(0);
        expect(sortComparators.prefix(b, a)).toBeGreaterThan(0);
        expect(sortComparators.prefix(a, a)).toBe(0);
    });

    it('pref: sorts numerically by pref field', () => {
        const a = makeRoute({ pref: 10 });
        const b = makeRoute({ pref: 200 });
        expect(sortComparators.pref(a, b)).toBeLessThan(0);
        expect(sortComparators.pref(b, a)).toBeGreaterThan(0);
    });

    it('pref: treats missing pref as 0', () => {
        const a = makeRoute({});
        const b = makeRoute({ pref: 5 });
        expect(sortComparators.pref(a, b)).toBeLessThan(0);
    });

    it('is_best: false sorts before true', () => {
        const a = makeRoute({ is_best: false });
        const b = makeRoute({ is_best: true });
        expect(sortComparators.is_best(a, b)).toBeLessThan(0);
    });
});
