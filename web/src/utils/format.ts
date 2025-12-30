/**
 * Format packets per second value for display.
 * Examples: "1.2K pps", "3.5M pps", "1.1G pps"
 */
export const formatPps = (pps: number): string => {
    if (pps < 1000) {
        return `${Math.round(pps)} pps`;
    }
    if (pps < 1_000_000) {
        return `${(pps / 1000).toFixed(1)}K pps`;
    }
    if (pps < 1_000_000_000) {
        return `${(pps / 1_000_000).toFixed(1)}M pps`;
    }
    return `${(pps / 1_000_000_000).toFixed(1)}G pps`;
};

/**
 * Format bytes per second value for display.
 * Examples: "500 B/s", "1.2 KB/s", "3.5 MB/s", "1.1 GB/s"
 */
export const formatBps = (bps: number): string => {
    if (bps < 1024) {
        return `${Math.round(bps)} B/s`;
    }
    if (bps < 1024 * 1024) {
        return `${(bps / 1024).toFixed(1)} KB/s`;
    }
    if (bps < 1024 * 1024 * 1024) {
        return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
    }
    return `${(bps / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
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
