import { cidrToIPRange, ipRangeToCIDRs, normalizeIPRange } from '@yanet/core/utils/netip';
import type { IPRangeWire } from '@yanet/core/utils/netip';

/**
 * Parse the FIB drawer's single range field, which accepts a CIDR
 * ("10.0.0.0/24"), an explicit range ("10.0.0.0 - 10.0.0.255"), or a bare
 * host address. Returns undefined for unparseable or reversed input.
 */
export const parseRangeInput = (text: string): IPRangeWire | undefined => {
    const trimmed = text.trim();
    if (!trimmed) return undefined;
    if (trimmed.includes('/')) {
        return cidrToIPRange(trimmed);
    }
    const dashIdx = trimmed.indexOf('-');
    if (dashIdx === -1) {
        return normalizeIPRange(trimmed, trimmed);
    }
    const from = trimmed.slice(0, dashIdx).trim();
    const to = trimmed.slice(dashIdx + 1).trim();
    return normalizeIPRange(from, to);
};

/** True when the text parses as a range field value (see parseRangeInput). */
export const isValidRangeInput = (text: string): boolean => !!parseRangeInput(text);

/** Format a range for display: as a CIDR when the endpoints align to one, else "from - to". */
export const formatRangeInput = (from: string, to: string): string => {
    if (!from && !to) return '';
    if (!from || !to) return `${from} - ${to}`;
    const cidrs = ipRangeToCIDRs({ start: from, end: to });
    return cidrs.length === 1 ? cidrs[0] : `${from} - ${to}`;
};
