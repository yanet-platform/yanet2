import type { Route } from '../../api/routes';
import {
    parseCIDRPrefix,
    parseIPAddress,
    CIDRParseError,
    IPParseError,
} from '../../utils';

export const getRouteId = (route: Route): string => {
    return `${route.prefix || ''}_${route.nextHop || ''}_${route.peer || ''}_${route.routeDistinguisher || ''}`;
};

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

export const formatRouteCount = (count: number): string => {
    if (count === 1) return 'route';
    return 'routes';
};
