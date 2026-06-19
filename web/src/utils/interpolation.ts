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
 * Minimum elapsed time (in seconds) required to compute a valid rate sample.
 *
 * Samples with a shorter dt are discarded to prevent division by a tiny
 * number producing physically-impossible rates when two fetches settle close
 * together.
 */
export const MIN_DT_SECONDS = 0.1;

/**
 * Compute per-second packet and byte rates from two cumulative counter snapshots.
 *
 * Returns null when dtSeconds is below MIN_DT_SECONDS (including zero and
 * negative values) so the caller can carry forward the previous sample instead
 * of publishing a bogus rate. A negative delta (counter reset) is mapped to 0
 * on the normal path rather than producing a large negative spike.
 */
export const computeRate = (
    prev: { packets: bigint; bytes: bigint },
    cur: { packets: bigint; bytes: bigint },
    dtSeconds: number,
): { pps: number; bps: number } | null => {
    if (dtSeconds < MIN_DT_SECONDS) return null;
    const dp = Number(cur.packets - prev.packets);
    const db = Number(cur.bytes - prev.bytes);
    return {
        pps: dp >= 0 ? dp / dtSeconds : 0,
        bps: db >= 0 ? db / dtSeconds : 0,
    };
};
