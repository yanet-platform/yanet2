/**
 * Gets MAC address value for sorting/comparison.
 *
 * @param addr - MAC address as string in format "xx:xx:xx:xx:xx:xx"
 * @returns MAC address string (empty string if undefined)
 */
export function getMACAddressValue(addr: string | undefined): string {
    return addr || '';
}

/**
 * Compares two MAC address strings for sorting.
 * Uses lexicographic comparison of MAC address strings.
 *
 * @param valA - First MAC address string
 * @param valB - Second MAC address string
 * @returns Comparison result: negative if valA < valB, positive if valA > valB, 0 if equal
 */
export function compareMACAddressValues(valA: string, valB: string): number {
    return valA.localeCompare(valB);
}

/**
 * Formats a MAC address string (pass-through).
 * MAC addresses are now stored as strings in the correct format.
 *
 * @param addr - MAC address string in format "xx:xx:xx:xx:xx:xx"
 * @returns The same MAC address string
 */
export function formatMACAddress(addr: string): string {
    return addr;
}

/**
 * Format MAC address from bytes array to string
 * @param bytes - Array of 6 bytes representing MAC address
 * @returns MAC address string in format "xx:xx:xx:xx:xx:xx"
 */
export const formatMACFromBytes = (bytes: number[]): string => {
    if (bytes.length !== 6) return '';
    return bytes.map((b) => b.toString(16).padStart(2, '0')).join(':');
};

/**
 * Parse MAC address string to bytes array.
 * Supports multiple formats: colon-separated, hyphen-separated, dot-separated (Cisco), no separator.
 *
 * @param mac - MAC address string
 * @returns Array of 6 bytes or undefined if invalid
 */
export const parseMACToBytes = (mac: string): number[] | undefined => {
    const trimmed = mac.trim();
    if (!trimmed) return undefined;

    if (trimmed.includes(':') || trimmed.includes('-')) {
        const parts = trimmed.split(/[:-]/);
        if (parts.length !== 6) return undefined;

        const bytes: number[] = [];
        for (const part of parts) {
            const num = parseInt(part, 16);
            if (isNaN(num) || num < 0 || num > 255) return undefined;
            bytes.push(num);
        }
        return bytes;
    }

    if (trimmed.includes('.')) {
        const parts = trimmed.split('.');
        if (parts.length !== 3) return undefined;

        const bytes: number[] = [];
        for (const part of parts) {
            if (part.length !== 4) return undefined;
            const val = parseInt(part, 16);
            if (isNaN(val)) return undefined;
            bytes.push((val >> 8) & 0xff);
            bytes.push(val & 0xff);
        }
        return bytes;
    }

    if (trimmed.length === 12) {
        const bytes: number[] = [];
        for (let i = 0; i < 6; i++) {
            const num = parseInt(trimmed.substring(i * 2, i * 2 + 2), 16);
            if (isNaN(num)) return undefined;
            bytes.push(num);
        }
        return bytes;
    }

    return undefined;
};

/**
 * Validates a MAC address string.
 * Supports multiple formats: colon-separated, hyphen-separated, dot-separated (Cisco), no separator.
 *
 * @param mac - MAC address string to validate
 * @returns true if valid MAC address, false otherwise
 */
export const isValidMAC = (mac: string): boolean => {
    return parseMACToBytes(mac) !== undefined;
};
