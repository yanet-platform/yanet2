import { isValidCIDRPrefix, isValidIPAddress } from './netip';

/**
 * Validate CIDR string (IPv4 or IPv6). Returns true if valid.
 *
 * The mask is optional — delegates to netip.ts's strict parsers, accepting
 * either a bare host address or a `/mask` prefix.
 */
export const isValidCidr = (s: string): boolean => {
    const trimmed = s.trim();
    return isValidCIDRPrefix(trimmed) || isValidIPAddress(trimmed);
};

/**
 * Validate a CIDR prefix (IPv4 or IPv6) that must include a /mask.
 *
 * Unlike isValidCidr, the mask is mandatory — bare host addresses are
 * rejected.
 */
export const isValidCidrPrefix = (s: string): boolean => isValidCIDRPrefix(s.trim());

/** Validate device name string. */
export const isValidDeviceName = (s: string): boolean => /^[a-zA-Z0-9_:.\-]+$/.test(s.trim());

/** Returns true if any field in the errors object has a truthy value. */
export const rowHasError = <E extends object>(errs: E): boolean =>
    Object.values(errs).some(Boolean);

/** Count rows that fail validation according to the provided validate function. */
export const countInvalidRows = <T, E extends object>(
    rows: T[],
    validate: (row: T) => E,
): number => rows.filter((row) => rowHasError(validate(row))).length;
