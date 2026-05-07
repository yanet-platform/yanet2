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
    if (bytes < BigInt(1024)) {
        return `${bytes} B`;
    }
    if (bytes < BigInt(1024 * 1024)) {
        const kb = Number(bytes) / 1024;
        return `${kb.toFixed(1)} KB`;
    }
    if (bytes < BigInt(1024 * 1024 * 1024)) {
        const mb = Number(bytes) / (1024 * 1024);
        return `${mb.toFixed(1)} MB`;
    }
    if (bytes < BigInt(1024 * 1024 * 1024 * 1024)) {
        const gb = Number(bytes) / (1024 * 1024 * 1024);
        return `${gb.toFixed(2)} GB`;
    }
    const tb = Number(bytes) / (1024 * 1024 * 1024 * 1024);
    return `${tb.toFixed(2)} TB`;
};
