// Result types for error handling
export type Ok<T> = { ok: true; value: T };
export type Err<E> = { ok: false; error: E };
export type Result<T, E> = Ok<T> | Err<E>;

export function ok<T>(value: T): Ok<T> {
    return { ok: true, value };
}

export function err<E>(error: E): Err<E> {
    return { ok: false, error };
}

// Error types for IP parsing
export enum IPv4ParseError {
    EmptyString = 'empty_string',
    InvalidFormat = 'invalid_format',
    InvalidOctet = 'invalid_octet',
    LeadingZero = 'leading_zero'
}

export enum IPv6ParseError {
    EmptyString = 'empty_string',
    InvalidFormat = 'invalid_format',
    TooManyDoubleColons = 'too_many_double_colons',
    InvalidCompression = 'invalid_compression',
    EndsWithColon = 'ends_with_colon'
}

export enum CIDRParseError {
    EmptyString = 'empty_string',
    InvalidFormat = 'invalid_format',
    InvalidPrefixLength = 'invalid_prefix_length',
    InvalidIPAddress = 'invalid_ip_address'
}

/**
 * IPv4 Address class with validation
 */
export class IPv4Address {
    private constructor(public readonly octets: [number, number, number, number]) { }

    /**
     * Parse IPv4 address string into IPv4Address object
     */
    static parse(ip: string): Result<IPv4Address, IPv4ParseError> {
        if (!ip || typeof ip !== 'string') {
            return err(IPv4ParseError.EmptyString);
        }

        try {
            // Use URL constructor to validate - it throws for invalid IPs
            new URL(`http://${ip}`);
            // Additional check: ensure it doesn't contain port or path
            const parts = ip.split('.');
            if (parts.length !== 4) {
                return err(IPv4ParseError.InvalidFormat);
            }

            // Check each octet is between 0-255 and has no leading zeros
            const octets: number[] = [];
            for (const octet of parts) {
                if (octet.length > 1 && octet.startsWith('0')) {
                    return err(IPv4ParseError.LeadingZero);
                }
                const num = parseInt(octet, 10);
                if (isNaN(num) || num < 0 || num > 255) {
                    return err(IPv4ParseError.InvalidOctet);
                }
                if (octet !== num.toString()) {
                    return err(IPv4ParseError.InvalidFormat);
                }
                octets.push(num);
            }

            return ok(new IPv4Address(octets as [number, number, number, number]));
        } catch {
            return err(IPv4ParseError.InvalidFormat);
        }
    }

    /**
     * Convert to string representation
     */
    toString(): string {
        return this.octets.join('.');
    }

    /**
     * Get the numeric value (useful for comparisons/sorting)
     */
    toNumber(): number {
        return (this.octets[0] << 24) | (this.octets[1] << 16) | (this.octets[2] << 8) | this.octets[3];
    }
}

/**
 * Validates if a string is a valid IPv4 address.
 *
 * @param ip - IP address string to validate
 * @returns true if valid IPv4 address, false otherwise
 */
export function isValidIPv4Address(ip: string): boolean {
    const result = IPv4Address.parse(ip);
    return result.ok;
}

/**
 * IPv6 Address class with validation
 */
export class IPv6Address {
    private constructor(public readonly groups: number[]) { }

    /**
     * Parse IPv6 address string into IPv6Address object
     */
    static parse(ip: string): Result<IPv6Address, IPv6ParseError> {
        if (!ip || typeof ip !== 'string') {
            return err(IPv6ParseError.EmptyString);
        }

        try {
            // Use URL constructor to validate - it throws for invalid IPs
            new URL(`http://[${ip}]`);

            const parts = ip.split(':');
            const doubleColonCount = (ip.match(/::/g) || []).length;

            // Can't have more than one ::
            if (doubleColonCount > 1) {
                return err(IPv6ParseError.TooManyDoubleColons);
            }

            // Count non-empty parts
            const nonEmptyParts = parts.filter(part => part.length > 0);

            // If no ::, should have exactly 8 parts
            if (doubleColonCount === 0 && nonEmptyParts.length !== 8) {
                return err(IPv6ParseError.InvalidFormat);
            }

            // If :: present, total parts should be <= 8 when expanded
            if (doubleColonCount === 1) {
                const totalParts = parts.length - 1; // subtract 1 for the empty part from ::
                if (totalParts > 8) {
                    return err(IPv6ParseError.InvalidCompression);
                }
            }

            // Parse groups (expand :: to zeros)
            const expanded = ip.replace('::', ':0000'.repeat(9 - parts.length)).split(':');
            const groups: number[] = [];
            for (const group of expanded) {
                const num = parseInt(group || '0', 16);
                if (isNaN(num) || num < 0 || num > 0xFFFF) {
                    return err(IPv6ParseError.InvalidFormat);
                }
                groups.push(num);
            }

            return ok(new IPv6Address(groups));
        } catch {
            return err(IPv6ParseError.InvalidFormat);
        }
    }

    /**
     * Convert to string representation (compressed format)
     */
    toString(): string {
        // Find longest sequence of zeros for compression
        let bestStart = -1;
        let bestLength = 0;
        let currentStart = -1;
        let currentLength = 0;

        for (let i = 0; i < this.groups.length; i++) {
            if (this.groups[i] === 0) {
                if (currentStart === -1) {
                    currentStart = i;
                    currentLength = 1;
                } else {
                    currentLength++;
                }
            } else {
                if (currentLength > bestLength) {
                    bestStart = currentStart;
                    bestLength = currentLength;
                }
                currentStart = -1;
                currentLength = 0;
            }
        }

        // Check the last sequence
        if (currentLength > bestLength) {
            bestStart = currentStart;
            bestLength = currentLength;
        }

        // Build the string
        const parts: string[] = [];
        for (let i = 0; i < this.groups.length; i++) {
            if (bestStart !== -1 && i >= bestStart && i < bestStart + bestLength) {
                if (i === bestStart) {
                    parts.push('');
                }
                continue;
            }
            parts.push(this.groups[i].toString(16));
        }

        return parts.join(':');
    }

    /**
     * Get the IPv6 address as a BigInt (useful for comparisons)
     */
    toBigInt(): bigint {
        let result = 0n;
        for (let i = 0; i < this.groups.length; i++) {
            result |= BigInt(this.groups[i]) << BigInt((7 - i) * 16);
        }
        return result;
    }
}

/**
 * Validates if a string is a valid IPv6 address.
 *
 * @param ip - IP address string to validate
 * @returns true if valid IPv6 address, false otherwise
 */
export function isValidIPv6Address(ip: string): boolean {
    const result = IPv6Address.parse(ip);
    return result.ok;
}

/**
 * IPv4 CIDR Prefix class
 */
export class IPv4Prefix {
    constructor(public readonly address: IPv4Address, public readonly prefixLength: number) { }

    /**
     * Parse IPv4 CIDR prefix string into IPv4Prefix object
     */
    static parse(prefix: string): Result<IPv4Prefix, CIDRParseError> {
        if (!prefix || typeof prefix !== 'string') {
            return err(CIDRParseError.EmptyString);
        }

        const parts = prefix.split('/');
        if (parts.length !== 2) {
            return err(CIDRParseError.InvalidFormat);
        }

        const [ip, maskStr] = parts;
        const mask = parseInt(maskStr, 10);

        // Check mask is valid for IPv4 (0-32)
        if (isNaN(mask) || mask < 0 || mask > 32) {
            return err(CIDRParseError.InvalidPrefixLength);
        }

        const addressResult = IPv4Address.parse(ip);
        if (!addressResult.ok) {
            return err(CIDRParseError.InvalidIPAddress);
        }

        return ok(new IPv4Prefix(addressResult.value, mask));
    }

    /**
     * Convert to string representation
     */
    toString(): string {
        return `${this.address.toString()}/${this.prefixLength}`;
    }
}

/**
 * IPv6 CIDR Prefix class
 */
export class IPv6Prefix {
    constructor(public readonly address: IPv6Address, public readonly prefixLength: number) { }

    /**
     * Parse IPv6 CIDR prefix string into IPv6Prefix object
     */
    static parse(prefix: string): Result<IPv6Prefix, CIDRParseError> {
        if (!prefix || typeof prefix !== 'string') {
            return err(CIDRParseError.EmptyString);
        }

        const parts = prefix.split('/');
        if (parts.length !== 2) {
            return err(CIDRParseError.InvalidFormat);
        }

        const [ip, maskStr] = parts;
        const mask = parseInt(maskStr, 10);

        // Check mask is valid for IPv6 (0-128)
        if (isNaN(mask) || mask < 0 || mask > 128) {
            return err(CIDRParseError.InvalidPrefixLength);
        }

        const addressResult = IPv6Address.parse(ip);
        if (!addressResult.ok) {
            return err(CIDRParseError.InvalidIPAddress);
        }

        return ok(new IPv6Prefix(addressResult.value, mask));
    }

    /**
     * Convert to string representation
     */
    toString(): string {
        return `${this.address.toString()}/${this.prefixLength}`;
    }
}

/**
 * Validates if a string is a valid IPv4 CIDR prefix.
 *
 * @param prefix - CIDR prefix string (e.g., "192.168.1.0/24")
 * @returns true if valid IPv4 CIDR prefix, false otherwise
 */
export function isValidIPv4Prefix(prefix: string): boolean {
    const result = IPv4Prefix.parse(prefix);
    return result.ok;
}

/**
 * Validates if a string is a valid IPv6 CIDR prefix.
 *
 * @param prefix - CIDR prefix string (e.g., "2001:db8::/32")
 * @returns true if valid IPv6 CIDR prefix, false otherwise
 */
export function isValidIPv6Prefix(prefix: string): boolean {
    const result = IPv6Prefix.parse(prefix);
    return result.ok;
}

// Union types for IP addresses and prefixes
export type IPAddress = IPv4Address | IPv6Address;
export type CIDRPrefix = IPv4Prefix | IPv6Prefix;

// Error types for generic parsing
export enum IPParseError {
    EmptyString = 'empty_string',
    InvalidFormat = 'invalid_format'
}

/**
 * Parse IP address string (IPv4 or IPv6) into IPAddress object
 */
export function parseIPAddress(ip: string): Result<IPAddress, IPParseError> {
    const ipv4Result = IPv4Address.parse(ip);
    if (ipv4Result.ok) {
        return ok(ipv4Result.value);
    }

    const ipv6Result = IPv6Address.parse(ip);
    if (ipv6Result.ok) {
        return ok(ipv6Result.value);
    }

    return err(IPParseError.InvalidFormat);
}

/**
 * Parse CIDR prefix string (IPv4 or IPv6) into CIDRPrefix object
 */
export function parseCIDRPrefix(prefix: string): Result<CIDRPrefix, CIDRParseError> {
    const ipv4Result = IPv4Prefix.parse(prefix);
    if (ipv4Result.ok) {
        return ok(ipv4Result.value);
    }

    const ipv6Result = IPv6Prefix.parse(prefix);
    if (ipv6Result.ok) {
        return ok(ipv6Result.value);
    }

    return err(CIDRParseError.InvalidFormat);
}

/**
 * Validates if a string is a valid IP address (IPv4 or IPv6).
 *
 * @param ip - IP address string to validate
 * @returns true if valid IP address, false otherwise
 */
export function isValidIPAddress(ip: string): boolean {
    const result = parseIPAddress(ip);
    return result.ok;
}

/**
 * Validates if a string is a valid CIDR prefix (IPv4 or IPv6).
 *
 * @param prefix - CIDR prefix string to validate
 * @returns true if valid CIDR prefix, false otherwise
 */
export function isValidCIDRPrefix(prefix: string): boolean {
    const result = parseCIDRPrefix(prefix);
    return result.ok;
}

/**
 * Extracts prefix length from a CIDR prefix string.
 *
 * @param prefix - CIDR prefix string (e.g., "192.168.1.0/24")
 * @returns prefix length as number, or null if invalid
 */
export function getPrefixLength(prefix: string): number | null {
    const result = parseCIDRPrefix(prefix);
    if (result.ok) {
        return result.value.prefixLength;
    }
    return null;
}

// ============================================================================
// Bytes-based IP address utilities
// ============================================================================

/**
 * Format IPv4 address from bytes array to string
 * @param bytes - Array of 4 bytes representing IPv4 address
 * @returns IPv4 address string (e.g., "192.168.1.1")
 */
export const formatIPv4FromBytes = (bytes: number[]): string => {
    if (bytes.length !== 4) return '';
    return bytes.join('.');
};

/**
 * Format IPv6 address from bytes array to string with :: compression
 * @param bytes - Array of 16 bytes representing IPv6 address
 * @returns IPv6 address string with :: compression
 */
export const formatIPv6FromBytes = (bytes: number[]): string => {
    if (bytes.length !== 16) return '';

    // Build array of 16-bit parts
    const parts: number[] = [];
    for (let i = 0; i < 16; i += 2) {
        parts.push((bytes[i] << 8) | bytes[i + 1]);
    }

    // Find longest run of zeros for :: compression
    let longestStart = -1;
    let longestLen = 0;
    let currentStart = -1;
    let currentLen = 0;

    for (let i = 0; i < 8; i++) {
        if (parts[i] === 0) {
            if (currentStart === -1) {
                currentStart = i;
                currentLen = 1;
            } else {
                currentLen++;
            }
        } else {
            if (currentLen > longestLen && currentLen > 1) {
                longestStart = currentStart;
                longestLen = currentLen;
            }
            currentStart = -1;
            currentLen = 0;
        }
    }
    // Check at end
    if (currentLen > longestLen && currentLen > 1) {
        longestStart = currentStart;
        longestLen = currentLen;
    }

    // Build string
    if (longestStart === -1) {
        // No compression
        return parts.map((p) => p.toString(16)).join(':');
    }

    const left = parts
        .slice(0, longestStart)
        .map((p) => p.toString(16))
        .join(':');
    const right = parts
        .slice(longestStart + longestLen)
        .map((p) => p.toString(16))
        .join(':');

    if (longestStart === 0 && longestLen === 8) {
        return '::';
    } else if (longestStart === 0) {
        return '::' + right;
    } else if (longestStart + longestLen === 8) {
        return left + '::';
    } else {
        return left + '::' + right;
    }
};

/**
 * Format IP address from bytes array (auto-detects IPv4 or IPv6)
 * @param bytes - Array of 4 or 16 bytes
 * @returns IP address string
 */
export const formatIPFromBytes = (bytes: number[]): string => {
    if (bytes.length === 4) {
        return formatIPv4FromBytes(bytes);
    }
    if (bytes.length === 16) {
        return formatIPv6FromBytes(bytes);
    }
    return '';
};

/**
 * Parse IPv4 string to bytes array
 * @param ipStr - IPv4 address string
 * @returns Array of 4 bytes or undefined if invalid
 */
export const parseIPv4ToBytes = (ipStr: string): number[] | undefined => {
    const parts = ipStr.split('.');
    if (parts.length !== 4) {
        return undefined;
    }
    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part, 10);
        if (isNaN(num) || num < 0 || num > 255) {
            return undefined;
        }
        bytes.push(num);
    }
    return bytes;
};

/**
 * Parse IPv6 string to bytes array
 * @param ipStr - IPv6 address string
 * @returns Array of 16 bytes or undefined if invalid
 */
export const parseIPv6ToBytes = (ipStr: string): number[] | undefined => {
    const trimmed = ipStr.trim();
    if (!trimmed) return undefined;

    // Handle :: expansion
    let fullAddr = trimmed;
    if (trimmed.includes('::')) {
        const parts = trimmed.split('::');
        const left = parts[0] ? parts[0].split(':') : [];
        const right = parts[1] ? parts[1].split(':') : [];
        const missing = 8 - left.length - right.length;
        if (missing < 0) return undefined;
        const middle = Array(missing).fill('0');
        fullAddr = [...left, ...middle, ...right].join(':');
    }

    const parts = fullAddr.split(':');
    if (parts.length !== 8) return undefined;

    const bytes: number[] = [];
    for (const part of parts) {
        const num = parseInt(part || '0', 16);
        if (isNaN(num) || num < 0 || num > 0xffff) return undefined;
        bytes.push((num >> 8) & 0xff);
        bytes.push(num & 0xff);
    }
    return bytes;
};

/**
 * Parse IP address string to bytes array (auto-detects IPv4 or IPv6)
 * @param ipStr - IP address string
 * @returns Array of bytes or undefined if invalid
 */
export const parseIPToBytes = (ipStr: string): number[] | undefined => {
    if (ipStr.includes(':')) {
        return parseIPv6ToBytes(ipStr);
    }
    return parseIPv4ToBytes(ipStr);
};

// ============================================================================
// Network mask utilities
// ============================================================================

/**
 * Check if mask is contiguous (all 1s followed by all 0s)
 * @param maskBytes - Array of bytes representing the mask
 * @returns true if mask is contiguous
 */
export const isContiguousMask = (maskBytes: number[]): boolean => {
    let foundZero = false;
    for (const byte of maskBytes) {
        for (let bit = 7; bit >= 0; bit--) {
            const isSet = (byte & (1 << bit)) !== 0;
            if (foundZero && isSet) {
                return false; // Found 1 after 0 - non-contiguous
            }
            if (!isSet) {
                foundZero = true;
            }
        }
    }
    return true;
};

/**
 * Count prefix length from contiguous mask bytes
 * @param maskBytes - Array of bytes representing the mask
 * @returns Number of leading 1 bits
 */
export const countPrefixLength = (maskBytes: number[]): number => {
    let prefixLen = 0;
    for (const byte of maskBytes) {
        for (let bit = 7; bit >= 0; bit--) {
            if (byte & (1 << bit)) {
                prefixLen++;
            } else {
                return prefixLen;
            }
        }
    }
    return prefixLen;
};

/**
 * Create mask bytes from prefix length
 * @param prefixLen - Number of leading 1 bits
 * @param totalBytes - Total number of bytes (4 for IPv4, 16 for IPv6)
 * @returns Array of mask bytes
 */
export const prefixLengthToMaskBytes = (prefixLen: number, totalBytes: number): number[] => {
    const mask: number[] = [];
    let remaining = prefixLen;
    for (let i = 0; i < totalBytes; i++) {
        if (remaining >= 8) {
            mask.push(255);
            remaining -= 8;
        } else if (remaining > 0) {
            mask.push((0xff << (8 - remaining)) & 0xff);
            remaining = 0;
        } else {
            mask.push(0);
        }
    }
    return mask;
};

/**
 * Format IP network (address + mask) to human-readable string
 * Supports both contiguous (CIDR) and non-contiguous masks
 * @param addrBytes - Array of bytes for address
 * @param maskBytes - Array of bytes for mask (optional)
 * @returns Formatted network string (e.g., "192.168.1.0/24" or "192.168.1.0/255.255.255.0")
 */
export const formatIPNet = (
    addrBytes: number[],
    maskBytes?: number[]
): string => {
    if (addrBytes.length === 0) return '';

    const ipStr = formatIPFromBytes(addrBytes);
    if (!maskBytes || maskBytes.length === 0) return ipStr;

    if (isContiguousMask(maskBytes)) {
        const prefixLen = countPrefixLength(maskBytes);
        return `${ipStr}/${prefixLen}`;
    } else {
        // Non-contiguous mask - show as IP/mask
        const maskStr = formatIPFromBytes(maskBytes);
        return `${ipStr}/${maskStr}`;
    }
};
