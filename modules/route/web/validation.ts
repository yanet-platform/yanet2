import type { FIBRowItem, FIBRowErrors } from './types';
import { normalizeIPRange } from '@yanet/core/utils/netip';
import { rowHasError as sharedRowHasError, countInvalidRows as sharedCountInvalidRows } from '@yanet/core/utils';

// Mirrors the registry-side counter-name limit: COUNTER_NAME_LEN is
// 128 minus the three uint64_t fields of struct counter, leaving 103
// usable bytes (see lib/counters/counters.h).
const MAX_COUNTER_NAME_BYTES = 103;

/** Returns true if the row's from/to parse as a valid, non-reversed IP range. */
export const isValidRange = (row: Pick<FIBRowItem, 'from' | 'to'>): boolean =>
    !!normalizeIPRange(row.from, row.to);

/** Returns true if s is a valid MAC address (colon-separated hex). */
export const isValidMac = (s: string): boolean =>
    /^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/.test(s || '');

/** Returns true if s is a valid network device name. */
export const isValidDevice = (s: string): boolean =>
    !!(s && /^[A-Za-z0-9_.\-]+$/.test(s));

/** Returns the counter field's error, or null when empty (server-generated) or valid. */
const counterError = (s: string): string | null => {
    if (!s) return null;
    if (!s.startsWith('nexthop_')) return 'Must start with nexthop_';
    if (new TextEncoder().encode(s).length > MAX_COUNTER_NAME_BYTES) return 'Too long (max 103 bytes)';
    return null;
};

/** Validate all fields of a FIB row. Returns null per field if valid. */
export const validateRow = (row: FIBRowItem): FIBRowErrors => ({
    range: isValidRange(row) ? null : ((row.from || row.to) ? 'Invalid range' : 'Required'),
    dst_mac: isValidMac(row.dst_mac) ? null : (row.dst_mac ? 'Invalid MAC' : 'Required'),
    src_mac: isValidMac(row.src_mac) ? null : (row.src_mac ? 'Invalid MAC' : 'Required'),
    device: isValidDevice(row.device) ? null : (row.device ? 'Invalid device name' : 'Required'),
    counter: counterError(row.counter),
});

/** Returns true if the row has any validation error. */
export const rowHasError = (row: FIBRowItem): boolean => sharedRowHasError(validateRow(row));

/** Count invalid rows in a list. */
export const countInvalidRows = (rows: FIBRowItem[]): number =>
    sharedCountInvalidRows(rows, validateRow);
