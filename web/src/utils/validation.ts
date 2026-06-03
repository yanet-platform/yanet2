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

/** Validate device name string. */
export const isValidDeviceName = (s: string): boolean => /^[a-zA-Z0-9_:.\-]+$/.test(s.trim());
