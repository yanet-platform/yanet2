/**
 * Decode base64 string to bytes array
 * @param base64 - Base64 encoded string
 * @returns Array of bytes
 */
export const base64ToBytes = (base64: string): number[] => {
    try {
        const binary = atob(base64);
        const bytes: number[] = [];
        for (let i = 0; i < binary.length; i++) {
            bytes.push(binary.charCodeAt(i));
        }
        return bytes;
    } catch {
        return [];
    }
};

/**
 * Encode bytes array to base64 string
 * @param bytes - Array of bytes
 * @returns Base64 encoded string
 */
export const bytesToBase64 = (bytes: number[]): string => {
    const uint8 = new Uint8Array(bytes);
    let binary = '';
    for (let i = 0; i < uint8.length; i++) {
        binary += String.fromCharCode(uint8[i]);
    }
    return btoa(binary);
};

/**
 * Get bytes from various input types (base64 string, Uint8Array, or number array)
 * @param input - Input in various formats
 * @returns Array of bytes
 */
export const getBytes = (input: string | Uint8Array | number[] | undefined): number[] => {
    if (!input) return [];
    if (typeof input === 'string') {
        return base64ToBytes(input);
    }
    return Array.from(input);
};

/**
 * Format a byte count using binary prefixes.
 * Examples: "500 B", "1.2 KB", "3.5 MB", "1.1 GB", "0.50 TB".
 */
export const formatBytes = (bytes: bigint): string => {
    if (bytes < 1024n) {
        return `${bytes} B`;
    }
    const tiers: { divisor: number; label: string; digits: number }[] = [
        { divisor: 1024, label: 'KB', digits: 1 },
        { divisor: 1024 ** 2, label: 'MB', digits: 1 },
        { divisor: 1024 ** 3, label: 'GB', digits: 2 },
        { divisor: 1024 ** 4, label: 'TB', digits: 2 },
    ];
    for (let idx = 0; idx < tiers.length; idx++) {
        const tier = tiers[idx];
        const value = Number(bytes) / tier.divisor;
        const formatted = value.toFixed(tier.digits);
        // Rounding may push the value to the next prefix (e.g. 1023.99 KB → 1024.0 KB);
        // in that case fall through to the next tier so we report 1.0 MB instead.
        if (parseFloat(formatted) >= 1024 && idx < tiers.length - 1) {
            continue;
        }
        return `${formatted} ${tier.label}`;
    }
    const tb = Number(bytes) / 1024 ** 4;
    return `${tb.toFixed(2)} TB`;
};
