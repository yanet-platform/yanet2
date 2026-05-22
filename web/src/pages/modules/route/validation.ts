import type { FIBRowItem, FIBRowErrors } from './types';
import { isValidCIDR } from '../../_shared/draft/cidr';

/** Returns true if s is a valid IPv4 or IPv6 CIDR prefix. */
export const isValidPrefix = isValidCIDR;

/** Returns true if s is a valid MAC address (colon-separated hex). */
export const isValidMac = (s: string): boolean =>
    /^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/.test(s || '');

/** Returns true if s is a valid network device name. */
export const isValidDevice = (s: string): boolean =>
    !!(s && /^[A-Za-z0-9_.\-]+$/.test(s));

/** Validate all fields of a FIB row. Returns null per field if valid. */
export const validateRow = (row: FIBRowItem): FIBRowErrors => ({
    prefix: isValidPrefix(row.prefix) ? null : (row.prefix ? 'Invalid CIDR' : 'Required'),
    dst_mac: isValidMac(row.dst_mac) ? null : (row.dst_mac ? 'Invalid MAC' : 'Required'),
    src_mac: isValidMac(row.src_mac) ? null : (row.src_mac ? 'Invalid MAC' : 'Required'),
    device: isValidDevice(row.device) ? null : (row.device ? 'Invalid device name' : 'Required'),
});

/** Returns true if the row has any validation error. */
export const rowHasError = (row: FIBRowItem): boolean => {
    const errs = validateRow(row);
    return Object.values(errs).some(Boolean);
};

/** Count invalid rows in a list. */
export const countInvalidRows = (rows: FIBRowItem[]): number =>
    rows.filter(rowHasError).length;
