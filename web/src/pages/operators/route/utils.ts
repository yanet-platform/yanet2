import type { Route } from '../../../api/routes';
import { parseCIDRPrefix, parseIPAddress, CIDRParseError, IPParseError } from '../../../utils';
import { ipAddressToString, type IPAddressWire } from '../../../utils/netip';
import type { RouteSortableColumn, IPFamily } from './types';

export interface RouteSubmitParams {
    prefix: string;
    nexthopIps: IPAddressWire[];
    doFlush: boolean;
}

export type RouteSubmitOp =
    | { type: 'delete'; prefix: string; nexthop: IPAddressWire }
    | { type: 'insert'; prefix: string; nexthops: IPAddressWire[]; doFlush: boolean };

/** Plan API operations for submitting a route. In add mode, just insert.
 *
 * In edit mode, delete the original first when its key changed. The insert
 * carries all nexthop addresses for ECMP. */
export const planRouteSubmit = (
    mode: 'add' | 'edit',
    params: RouteSubmitParams,
    newNexthopStr: string,
    original: Route | null,
    originalNexthopStr: string,
): RouteSubmitOp[] => {
    const ops: RouteSubmitOp[] = [];
    const keyChanged = mode === 'edit'
        && !!original
        && (original.prefix !== params.prefix || originalNexthopStr !== newNexthopStr);
    if (keyChanged && original?.prefix && original.next_hop) {
        ops.push({ type: 'delete', prefix: original.prefix, nexthop: original.next_hop });
    }
    ops.push({ type: 'insert', prefix: params.prefix, nexthops: params.nexthopIps, doFlush: params.doFlush });
    return ops;
};

export const ROUTE_SOURCES = ['Unknown', 'Static', 'BIRD'] as const;

/** Returns a stable string key for a route row. */
export const getRouteId = (route: Route): string =>
    `${route.prefix ?? ''}_${String(route.next_hop?.addr ?? '')}_${String(route.peer?.addr ?? '')}_${route.route_distinguisher ?? ''}_${route.source ?? 0}_${route.pref ?? 0}_${route.peer_as ?? 0}_${route.origin_as ?? 0}_${route.med ?? 0}_${route.as_path_len ?? 0}`;

/** Validates a CIDR prefix string. Returns an error message or undefined when valid. */
export const validatePrefix = (prefix: string): string | undefined => {
    if (!prefix.trim()) {
        return undefined;
    }
    const result = parseCIDRPrefix(prefix);
    if (!result.ok) {
        switch (result.error) {
            case CIDRParseError.EmptyString:
                return 'Prefix cannot be empty';
            case CIDRParseError.InvalidFormat:
                return 'Invalid prefix format. Use CIDR notation (e.g., 192.168.1.0/24 or 2001:db8::/32)';
            case CIDRParseError.InvalidPrefixLength:
                return 'Invalid prefix length';
            case CIDRParseError.InvalidIPAddress:
                return 'Invalid IP address in prefix';
            default:
                return 'Invalid prefix format';
        }
    }
    return undefined;
};

/** Validates a next-hop IP address string. Returns an error message or undefined when valid. */
export const validateNexthop = (nexthop: string): string | undefined => {
    if (!nexthop.trim()) {
        return undefined;
    }
    const result = parseIPAddress(nexthop);
    if (!result.ok) {
        switch (result.error) {
            case IPParseError.EmptyString:
                return 'IP address cannot be empty';
            case IPParseError.InvalidFormat:
                return 'Invalid IP address format. Use valid IPv4 (e.g., 192.168.1.1) or IPv6 (e.g., 2001:db8::1) address';
            default:
                return 'Invalid IP address format';
        }
    }
    return undefined;
};

/** Returns the IP family of a prefix string. Detects ':' for IPv6, else IPv4. */
export const prefixIPFamily = (prefix: string): IPFamily => {
    return prefix.includes(':') ? 'v6' : 'v4';
};

/** Groups routes by prefix, returning a map from prefix → route array. */
export const groupByPrefix = (routes: Route[]): Map<string, Route[]> => {
    const m = new Map<string, Route[]>();
    for (const r of routes) {
        const key = r.prefix || '';
        const group = m.get(key);
        if (group) {
            group.push(r);
        } else {
            m.set(key, [r]);
        }
    }
    return m;
};

/** Filters routes by IP family. Returns all routes when family is 'all'. */
export const filterByFamily = (routes: Route[], family: IPFamily): Route[] => {
    if (family === 'all') return routes;
    return routes.filter((r) => prefixIPFamily(r.prefix || '') === family);
};

/** Returns a human-readable reason why the first candidate beats the second,
 *  walking the BGP decision ladder: pref > as_path_len > med > origin_as > peer_as. */
export const bestPathReason = (cands: Route[]): string => {
    if (cands.length < 2) return '';
    const best = cands[0];
    const runner = cands[1];

    const bestPref = best.pref ?? 0;
    const runnerPref = runner.pref ?? 0;
    if (bestPref !== runnerPref) {
        return `higher Local Pref (${bestPref} vs ${runnerPref})`;
    }

    const bestLen = best.as_path_len ?? 0;
    const runnerLen = runner.as_path_len ?? 0;
    if (bestLen !== runnerLen) {
        return `shorter AS path (${bestLen} vs ${runnerLen})`;
    }

    const bestMed = best.med ?? 0;
    const runnerMed = runner.med ?? 0;
    if (bestMed !== runnerMed) {
        return `lower MED (${bestMed} vs ${runnerMed})`;
    }

    const bestOrigin = best.origin_as ?? 0;
    const runnerOrigin = runner.origin_as ?? 0;
    if (bestOrigin !== runnerOrigin) {
        return `lower Origin AS (${bestOrigin} vs ${runnerOrigin})`;
    }

    const bestPeerAs = best.peer_as ?? 0;
    const runnerPeerAs = runner.peer_as ?? 0;
    if (bestPeerAs !== runnerPeerAs) {
        return `lower Peer AS (${bestPeerAs} vs ${runnerPeerAs})`;
    }

    return '';
};

/** Sort comparators for each sortable column. */
export const sortComparators: Record<RouteSortableColumn, (a: Route, b: Route) => number> = {
    prefix: (a, b) => (a.prefix || '').localeCompare(b.prefix || ''),
    next_hop: (a, b) => ipAddressToString(a.next_hop).localeCompare(ipAddressToString(b.next_hop)),
    peer: (a, b) => ipAddressToString(a.peer).localeCompare(ipAddressToString(b.peer)),
    is_best: (a, b) => (a.is_best ? 1 : 0) - (b.is_best ? 1 : 0),
    pref: (a, b) => (a.pref ?? 0) - (b.pref ?? 0),
    as_path_len: (a, b) => (a.as_path_len ?? 0) - (b.as_path_len ?? 0),
    source: (a, b) => (a.source ?? 0) - (b.source ?? 0),
};
