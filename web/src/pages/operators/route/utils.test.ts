import { describe, it, expect } from 'vitest';
import { planRouteSubmit, validatePrefix, validateNexthop, sortComparators, prefixIPFamily, groupByPrefix, filterByFamily, bestPathReason, getRouteId } from './utils';
import { stringToIPAddress } from '../../../utils/netip';
import type { Route } from '../../../api/routes';

// ---------------------------------------------------------------------------
// planRouteSubmit
// ---------------------------------------------------------------------------

describe('planRouteSubmit', () => {
    const ip = (s: string) => stringToIPAddress(s)!;

    it('add mode: produces a single insert op with one nexthop', () => {
        const ops = planRouteSubmit(
            'add',
            { prefix: '10.0.0.0/8', nexthopIps: [ip('192.168.1.1')], doFlush: false },
            '192.168.1.1',
            null,
            '',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
        if (ops[0].type === 'insert') {
            expect(ops[0].prefix).toBe('10.0.0.0/8');
            expect(ops[0].nexthops).toHaveLength(1);
            expect(ops[0].doFlush).toBe(false);
        }
    });

    it('add mode: produces a single insert op with multiple ECMP nexthops', () => {
        const ops = planRouteSubmit(
            'add',
            { prefix: '10.0.0.0/8', nexthopIps: [ip('192.168.1.1'), ip('192.168.1.2')], doFlush: false },
            '192.168.1.1',
            null,
            '',
        );
        expect(ops).toHaveLength(1);
        expect(ops[0].type).toBe('insert');
        if (ops[0].type === 'insert') {
            expect(ops[0].nexthops).toHaveLength(2);
        }
    });

    it('edit mode, no key change: produces a single insert op without delete', () => {
        const original: Route = { prefix: '10.0.0.0/8', next_hop: ip('192.168.1.1') };
        const ops = planRouteSubmit(
            'edit',
            { prefix: '10.0.0.0/8', nexthopIps: [ip('192.168.1.1')], doFlush: false },
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
            { prefix: '172.16.0.0/12', nexthopIps: [ip('192.168.1.1')], doFlush: false },
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
            { prefix: '10.0.0.0/8', nexthopIps: [ip('10.0.0.1')], doFlush: true },
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
            { prefix: '172.16.0.0/12', nexthopIps: [ip('10.0.0.1')], doFlush: false },
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
            { prefix: '10.0.0.0/8', nexthopIps: [ip('192.168.1.1')], doFlush: false },
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
            { prefix: '172.16.0.0/12', nexthopIps: [ip('192.168.1.1')], doFlush: false },
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

// ---------------------------------------------------------------------------
// prefixIPFamily
// ---------------------------------------------------------------------------

describe('prefixIPFamily', () => {
    it('returns v6 for IPv6 prefix', () => {
        expect(prefixIPFamily('::/0')).toBe('v6');
        expect(prefixIPFamily('2001:db8::/32')).toBe('v6');
    });

    it('returns v4 for IPv4 prefix', () => {
        expect(prefixIPFamily('0.0.0.0/0')).toBe('v4');
        expect(prefixIPFamily('192.168.1.0/24')).toBe('v4');
    });
});

// ---------------------------------------------------------------------------
// groupByPrefix
// ---------------------------------------------------------------------------

describe('groupByPrefix', () => {
    it('groups routes with the same prefix together', () => {
        const routes: Route[] = [
            { prefix: '10.0.0.0/8' },
            { prefix: '::/0' },
            { prefix: '10.0.0.0/8' },
        ];
        const m = groupByPrefix(routes);
        expect(m.get('10.0.0.0/8')).toHaveLength(2);
        expect(m.get('::/0')).toHaveLength(1);
    });

    it('returns empty map for empty input', () => {
        expect(groupByPrefix([])).toEqual(new Map());
    });
});

// ---------------------------------------------------------------------------
// filterByFamily
// ---------------------------------------------------------------------------

describe('filterByFamily', () => {
    const routes: Route[] = [
        { prefix: '::/0' },
        { prefix: '0.0.0.0/0' },
        { prefix: '2001:db8::/32' },
    ];

    it('returns all routes for family "all"', () => {
        expect(filterByFamily(routes, 'all')).toHaveLength(3);
    });

    it('returns only IPv6 routes for family "v6"', () => {
        const v6 = filterByFamily(routes, 'v6');
        expect(v6).toHaveLength(2);
        expect(v6.every((r) => r.prefix?.includes(':'))).toBe(true);
    });

    it('returns only IPv4 routes for family "v4"', () => {
        const v4 = filterByFamily(routes, 'v4');
        expect(v4).toHaveLength(1);
        expect(v4[0].prefix).toBe('0.0.0.0/0');
    });
});

// ---------------------------------------------------------------------------
// bestPathReason
// ---------------------------------------------------------------------------

describe('bestPathReason', () => {
    it('returns empty string for fewer than two candidates', () => {
        expect(bestPathReason([])).toBe('');
        expect(bestPathReason([{ pref: 100 }])).toBe('');
    });

    it('reports higher Local Pref as the deciding factor', () => {
        const best: Route = { pref: 100 };
        const other: Route = { pref: 0 };
        expect(bestPathReason([best, other])).toMatch(/Local Pref/);
        expect(bestPathReason([best, other])).toContain('100');
    });

    it('reports shorter AS path when prefs are equal', () => {
        const best: Route = { pref: 100, as_path_len: 2 };
        const other: Route = { pref: 100, as_path_len: 4 };
        expect(bestPathReason([best, other])).toMatch(/AS path/);
    });

    it('reports lower MED when pref and as_path_len are equal', () => {
        const best: Route = { pref: 100, as_path_len: 2, med: 0 };
        const other: Route = { pref: 100, as_path_len: 2, med: 50 };
        expect(bestPathReason([best, other])).toMatch(/MED/);
    });
});

// ---------------------------------------------------------------------------
// getRouteId
// ---------------------------------------------------------------------------

describe('getRouteId', () => {
    const base: Route = {
        prefix: '::/0',
        next_hop: { addr: 'fe80::a1a' },
        peer: { addr: '::' },
        route_distinguisher: '',
        pref: 100,
        peer_as: 65000,
        origin_as: 65001,
        med: 0,
        as_path_len: 2,
    };

    it('produces a stable id for a basic route', () => {
        const id = getRouteId(base);
        // Format: prefix_nextHop_peer_rd_source_pref_peerAs_originAs_med_asPathLen
        expect(id).toBe('::/0_fe80::a1a_::__0_100_65000_65001_0_2');
        expect(getRouteId(base)).toBe(id);
    });

    it('BIRD vs Static: same prefix/next_hop/peer but different source yields different ids', () => {
        const bird: Route = { ...base, source: 2 };   // RouteSourceID.BIRD
        const staticRoute: Route = { ...base, source: 1 }; // RouteSourceID.STATIC
        expect(getRouteId(bird)).not.toBe(getRouteId(staticRoute));
    });

    it('different pref yields different ids', () => {
        const a: Route = { ...base, pref: 100 };
        const b: Route = { ...base, pref: 200 };
        expect(getRouteId(a)).not.toBe(getRouteId(b));
    });

    it('different peer_as yields different ids', () => {
        const a: Route = { ...base, peer_as: 65000 };
        const b: Route = { ...base, peer_as: 65001 };
        expect(getRouteId(a)).not.toBe(getRouteId(b));
    });

    it('missing optional fields fall back to 0 or empty string, not "undefined"', () => {
        const minimal: Route = { prefix: '10.0.0.0/8' };
        const id = getRouteId(minimal);
        expect(id).not.toContain('undefined');
        // next_hop.addr='' peer.addr='' rd='' source=0 pref=0 peerAs=0 originAs=0 med=0 asPathLen=0
        expect(id).toBe('10.0.0.0/8____0_0_0_0_0_0');
    });
});
