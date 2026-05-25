/** Format a packet-per-second rate as a human-readable string. */
export const fmtPps = (n: number): string => {
    if (!n || !isFinite(n)) return '0';
    if (n >= 1e9) return `${(n / 1e9).toFixed(2)}G`;
    if (n >= 1e6) return `${(n / 1e6).toFixed(2)}M`;
    if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
    return Math.round(n).toString();
};

/** Format a bytes-per-second rate as bits-per-second human-readable string. */
export const fmtBps = (n: number): string => {
    if (!n || !isFinite(n)) return '0';
    const b = n * 8;
    if (b >= 1e12) return `${(b / 1e12).toFixed(2)}T`;
    if (b >= 1e9) return `${(b / 1e9).toFixed(2)}G`;
    if (b >= 1e6) return `${(b / 1e6).toFixed(1)}M`;
    if (b >= 1e3) return `${(b / 1e3).toFixed(1)}k`;
    return Math.round(b).toString();
};

/** Format byte count in IEC binary units (KiB/MiB/GiB), 1024-base. Matches CLI. */
export const fmtIEC = (n: number): string => {
    if (!n || !isFinite(n)) return '0 B';
    const KiB = 1024, MiB = KiB * 1024, GiB = MiB * 1024, TiB = GiB * 1024;
    if (n >= TiB) return `${(n / TiB).toFixed(2)} TiB`;
    if (n >= GiB) return `${(n / GiB).toFixed(1)} GiB`;
    if (n >= MiB) return `${(n / MiB).toFixed(1)} MiB`;
    if (n >= KiB) return `${(n / KiB).toFixed(1)} KiB`;
    return `${Math.round(n)} B`;
};
