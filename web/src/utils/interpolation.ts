/**
 * Pure interpolation math helpers — no React dependency.
 */

/** Linear interpolation between a and b by factor t (not clamped). */
export const lerp = (a: number, b: number, t: number): number => a + (b - a) * t;

/**
 * Compute normalised animation progress in [0, 1].
 *
 * Returns 0 when wall clock equals sampleTs and 1 when it reaches
 * sampleTs + intervalMs. Clamps strictly to [0, 1] so the displayed value
 * never overshoots the current sample.
 */
export const clampProgress = (now: number, sampleTs: number, intervalMs: number): number => {
    if (intervalMs <= 0) return 1;
    const elapsed = now - sampleTs;
    return Math.max(0, Math.min(1, elapsed / intervalMs));
};

/**
 * Compute per-second packet and byte rates from two cumulative counter
 * snapshots.
 *
 * A negative delta (counter reset) is mapped to 0 rather than producing a
 * large negative spike.
 */
export const computeRate = (
    prev: { packets: bigint; bytes: bigint },
    cur: { packets: bigint; bytes: bigint },
    dtSeconds: number,
): { pps: number; bps: number } => {
    if (dtSeconds <= 0) return { pps: 0, bps: 0 };
    const dp = Number(cur.packets - prev.packets);
    const db = Number(cur.bytes - prev.bytes);
    return {
        pps: dp >= 0 ? dp / dtSeconds : 0,
        bps: db >= 0 ? db / dtSeconds : 0,
    };
};
