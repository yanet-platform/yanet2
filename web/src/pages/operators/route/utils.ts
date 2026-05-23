import type { Route } from '../../../api/routes';
import { parseCIDRPrefix, parseIPAddress, CIDRParseError, IPParseError } from '../../../utils';
import { ipAddressToString, type IPAddressWire } from '../../../utils/netip';
import type { RouteSortableColumn } from './types';

export interface RouteSubmitParams {
    prefix: string;
    nexthopIp: IPAddressWire;
    doFlush: boolean;
}

export type RouteSubmitOp =
    | { type: 'delete'; prefix: string; nexthop: IPAddressWire }
    | { type: 'insert'; prefix: string; nexthop: IPAddressWire; doFlush: boolean };

/** Plan API operations for submitting a route. In add mode, just insert.
 *
 * In edit mode, delete the original first when its key changed. */
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
    ops.push({ type: 'insert', prefix: params.prefix, nexthop: params.nexthopIp, doFlush: params.doFlush });
    return ops;
};

export const ROUTE_SOURCES = ['Unknown', 'Static', 'BIRD'] as const;

/** Returns a stable string key for a route row. */
export const getRouteId = (route: Route): string =>
    `${route.prefix || ''}_${String(route.next_hop?.addr || '')}_${String(route.peer?.addr || '')}_${route.route_distinguisher || ''}`;

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
