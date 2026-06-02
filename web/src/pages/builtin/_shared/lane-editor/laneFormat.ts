/** Format a pps number with a K/M suffix. */
export const formatPps = (v: number): string => {
    if (v >= 1_000_000) {
        return `${(v / 1_000_000).toFixed(2)}M`;
    }
    if (v >= 1_000) {
        return `${(v / 1_000).toFixed(1)}K`;
    }
    return String(Math.round(v));
};
