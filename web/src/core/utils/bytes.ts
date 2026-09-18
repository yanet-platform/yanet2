/**
 * Format a byte count using IEC binary prefixes (1024-base).
 * Examples: "500 B", "1.2 KiB", "3.5 MiB", "1.1 GiB", "0.50 TiB".
 */
export const formatBytes = (bytes: bigint): string => {
    if (bytes < 1024n) {
        return `${bytes} B`;
    }
    const tiers: { divisor: number; label: string; digits: number }[] = [
        { divisor: 1024, label: 'KiB', digits: 1 },
        { divisor: 1024 ** 2, label: 'MiB', digits: 1 },
        { divisor: 1024 ** 3, label: 'GiB', digits: 2 },
        { divisor: 1024 ** 4, label: 'TiB', digits: 2 },
    ];
    for (let idx = 0; idx < tiers.length; idx++) {
        const tier = tiers[idx];
        const value = Number(bytes) / tier.divisor;
        const formatted = value.toFixed(tier.digits);
        // Rounding may push the value to the next prefix (e.g. 1023.99 KiB → 1024.0 KiB);
        // in that case fall through to the next tier so we report 1.0 MiB instead.
        if (parseFloat(formatted) >= 1024 && idx < tiers.length - 1) {
            continue;
        }
        return `${formatted} ${tier.label}`;
    }
    const tb = Number(bytes) / 1024 ** 4;
    return `${tb.toFixed(2)} TiB`;
};
