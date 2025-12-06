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
    private constructor(public readonly octets: [number, number, number, number]) {}

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
    private constructor(public readonly groups: number[]) {}

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
    constructor(public readonly address: IPv4Address, public readonly prefixLength: number) {}

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
    constructor(public readonly address: IPv6Address, public readonly prefixLength: number) {}

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
