/**
 * Gets numeric value of MAC address for sorting/comparison.
 * 
 * @param addr - MAC address as string (uint64 serialized as string), number, or bigint
 * @returns Numeric value as number (for values that fit in Number.MAX_SAFE_INTEGER) or bigint
 */
export function getMACAddressValue(addr: string | number | bigint | undefined): number | bigint {
    if (addr === undefined || addr === null) {
        return 0;
    }

    // Convert to BigInt, handling string representation of uint64
    const addrBigInt = typeof addr === 'string'
        ? BigInt(addr)
        : typeof addr === 'bigint'
            ? addr
            : BigInt(addr);

    // Convert to number if it fits, otherwise return bigint
    if (addrBigInt <= BigInt(Number.MAX_SAFE_INTEGER)) {
        return Number(addrBigInt);
    }
    return addrBigInt;
}

/**
 * Compares two MAC address values for sorting.
 * 
 * @param valA - First MAC address value
 * @param valB - Second MAC address value
 * @returns Comparison result: negative if valA < valB, positive if valA > valB, 0 if equal
 */
export function compareMACAddressValues(valA: number | bigint, valB: number | bigint): number {
    // Handle comparison of number and bigint
    if (typeof valA === 'bigint' && typeof valB === 'bigint') {
        if (valA === valB) return 0;
        return valA < valB ? -1 : 1;
    }
    if (typeof valA === 'bigint' || typeof valB === 'bigint') {
        const bigA = typeof valA === 'bigint' ? valA : BigInt(valA);
        const bigB = typeof valB === 'bigint' ? valB : BigInt(valB);
        if (bigA === bigB) return 0;
        return bigA < bigB ? -1 : 1;
    }
    // Both are numbers
    return Number(valA) - Number(valB);
}

/**
 * Formats a MAC address from uint64 to string format.
 * 
 * MAC address is stored as uint64 in big-endian format (network byte order).
 * First 6 bytes are the MAC address, last 2 bytes are zeros.
 * 
 * Example: MAC "3a:ac:26:9b:5b:f9" is stored as 0x3aac269b5bf90000
 * 
 * @param addr - MAC address as string (uint64 serialized as string), number, or bigint
 * @returns Formatted MAC address string in format "xx:xx:xx:xx:xx:xx"
 */
export function formatMACAddress(addr: string | number | bigint): string {
    // Convert to BigInt, handling string representation of uint64
    const addrBigInt = typeof addr === 'string'
        ? BigInt(addr)
        : typeof addr === 'bigint'
            ? addr
            : BigInt(addr);

    // Extract 6 bytes from big-endian representation
    // We need to extract bytes from positions 7 down to 2 (skipping the last 2 zero bytes)
    // In big-endian: byte 7 is most significant, byte 0 is least significant
    // MAC bytes are at positions 7, 6, 5, 4, 3, 2
    const bytes: string[] = [];
    for (let i = 7; i >= 2; i--) {
        const byte = Number((addrBigInt >> BigInt(i * 8)) & BigInt(0xff));
        bytes.push(byte.toString(16).padStart(2, '0'));
    }
    return bytes.join(':');
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
 * Parse MAC address string to bytes array
 * @param mac - MAC address string (supports : or - as separator)
 * @returns Array of 6 bytes or undefined if invalid
 */
export const parseMACToBytes = (mac: string): number[] | undefined => {
    const trimmed = mac.trim();
    if (!trimmed) return undefined;

    const parts = trimmed.split(/[:-]/);
    if (parts.length !== 6) return undefined;

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part, 16);
        if (isNaN(num) || num < 0 || num > 255) return undefined;
        bytes.push(num);
    }
    return bytes;
};
