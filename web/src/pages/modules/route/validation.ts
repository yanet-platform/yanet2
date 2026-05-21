import type { FIBRowItem, FIBRowErrors } from './types';

/** Returns true if s is a valid IPv4 CIDR. */
const isV4Cidr = (s: string): boolean => {
    const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/.exec(s);
    if (!m) return false;
    const octs = [m[1], m[2], m[3], m[4]].map(Number);
    if (octs.some((v) => v < 0 || v > 255)) return false;
    const prefix = Number(m[5]);
    return prefix >= 0 && prefix <= 32;
};

/** Returns true if s is a valid IPv6 CIDR (permissive). */
const isV6Cidr = (s: string): boolean => {
    const slash = s.indexOf('/');
    if (slash < 0) return false;
    const addr = s.slice(0, slash);
    const prefix = Number(s.slice(slash + 1));
    if (!Number.isFinite(prefix) || prefix < 0 || prefix > 128) return false;
    if (addr === '::') return true;
    if (!/^[0-9a-fA-F:]+$/.test(addr)) return false;
    const dcs = addr.match(/::/g);
    if (dcs && dcs.length > 1) return false;
    const parts = addr.split(':');
    if (!dcs && parts.length !== 8) return false;
    if (dcs && parts.length > 8) return false;
    for (const p of parts) {
        if (p === '') continue;
        if (p.length > 4) return false;
    }
    return true;
};

/** Returns true if s is a valid IPv4 or IPv6 CIDR prefix. */
export const isValidPrefix = (s: string): boolean => {
    if (!s) return false;
    return isV4Cidr(s) || isV6Cidr(s);
};

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
