import { getBigIntValue } from './sorting';

/**
 * Format an uint64-like value for display, returning "-" when null/undefined.
 */
export const formatUint64 = (value: string | number | bigint | undefined): string => {
    const parsed = getBigIntValue(value);
    return parsed === null ? '-' : parsed.toString();
};

interface FormatPpsOptions {
    /** Unit appended to every result. */
    unit?: string;
    /** Decimal places at the M tier. */
    mDecimals?: number;
    /** Largest tier to use; 'M' caps everything ≥ 1e6 at the M tier. */
    maxUnit?: 'M' | 'G';
}

/**
 * Format packets per second value for display.
 * Examples: "1.2K pps", "3.5M pps", "1.1G pps"
 */
export const formatPps = (pps: number, options: FormatPpsOptions = {}): string => {
    const { unit = ' pps', mDecimals = 1, maxUnit = 'G' } = options;
    if (pps < 1000) {
        return `${Math.round(pps)}${unit}`;
    }
    if (pps < 1_000_000) {
        return `${(pps / 1000).toFixed(1)}K${unit}`;
    }
    if (maxUnit === 'G' && pps >= 1_000_000_000) {
        return `${(pps / 1_000_000_000).toFixed(1)}G${unit}`;
    }
    return `${(pps / 1_000_000).toFixed(mDecimals)}M${unit}`;
};

/**
 * Format a large number with SI suffixes.
 * Examples: "1.2K", "3.5M", "1.1G"
 */
export const formatSiNumber = (value: number, suffix: string = ''): string => {
    if (value < 1000) {
        return `${Math.round(value)}${suffix ? ' ' + suffix : ''}`;
    }
    if (value < 1_000_000) {
        return `${(value / 1000).toFixed(1)}K${suffix ? ' ' + suffix : ''}`;
    }
    if (value < 1_000_000_000) {
        return `${(value / 1_000_000).toFixed(1)}M${suffix ? ' ' + suffix : ''}`;
    }
    return `${(value / 1_000_000_000).toFixed(1)}G${suffix ? ' ' + suffix : ''}`;
};

/**
 * Format bytes with binary prefixes.
 * Examples: "500 B", "1.2 KB", "3.5 MB", "1.1 GB"
 */
export const formatBytesRate = (bytes: number, suffix: string = '/s'): string => {
    if (bytes < 1024) {
        return `${Math.round(bytes)} B${suffix}`;
    }
    if (bytes < 1024 * 1024) {
        return `${(bytes / 1024).toFixed(1)} KB${suffix}`;
    }
    if (bytes < 1024 * 1024 * 1024) {
        return `${(bytes / (1024 * 1024)).toFixed(1)} MB${suffix}`;
    }
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB${suffix}`;
};

/**
 * Format bytes per second value for display.
 * Examples: "500 B/s", "1.2 KB/s", "3.5 MB/s", "1.1 GB/s"
 */
export const formatBps = (bps: number): string => formatBytesRate(bps);
