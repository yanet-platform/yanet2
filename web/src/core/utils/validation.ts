/** Validate CIDR string (IPv4 or IPv6). Returns true if valid. */
export const isValidCidr = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed) return false;
    const ipv4 = trimmed.match(/^(\d{1,3}(?:\.\d{1,3}){3})(?:\/(\d{1,2}))?$/);
    if (ipv4) {
        const parts = ipv4[1].split('.').map(Number);
        if (parts.some((n) => n > 255)) return false;
        if (ipv4[2] !== undefined && (Number(ipv4[2]) < 0 || Number(ipv4[2]) > 32)) return false;
        return true;
    }
    const ipv6 = trimmed.match(/^([0-9a-fA-F:]+)(?:\/(\d{1,3}))?$/);
    if (ipv6 && trimmed.includes(':')) {
        if (ipv6[2] !== undefined && (Number(ipv6[2]) < 0 || Number(ipv6[2]) > 128)) return false;
        return true;
    }
    return false;
};

/**
 * Validate a CIDR prefix (IPv4 or IPv6) that must include a /mask.
 *
 * Unlike isValidCidr, the mask is mandatory — bare host addresses are
 * rejected.
 */
export const isValidCidrPrefix = (s: string): boolean => {
    const trimmed = s.trim();
    if (!trimmed.includes('/')) return false;
    return isValidCidr(trimmed);
};

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
