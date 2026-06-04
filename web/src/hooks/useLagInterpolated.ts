import { useEffect, useRef, useState } from 'react';
import { lerp, clampProgress } from '../utils/interpolation';

interface KeyState {
    prevValue: number;
    curValue: number;
    curTs: number;
}

/**
 * Generic per-key lag-interpolation hook using requestAnimationFrame.
 *
 * For each key in `samples`, the returned map holds a value that
 * continuously interpolates from the previous sample toward the current
 * sample. Interpolation uses wall-clock progress clamped to [0, 1], so
 * the displayed value never overshoots the current sample (no extrapolation).
 *
 * Keys that disappear from the input map are removed from internal state
 * on the next RAF tick. The returned Map reference changes every animation
 * frame so memoized consumers detect the change.
 *
 * When there are no keys to animate, setState is skipped each frame to avoid
 * spurious re-renders (the last published empty map is already correct).
 */
export const useLagInterpolated = <K>(
    samples: Map<K, number>,
    intervalMs: number,
): Map<K, number> => {
    const stateRef = useRef<Map<K, KeyState>>(new Map());
    const lastInputRef = useRef<Map<K, number>>(new Map());
    const lastPublishedSizeRef = useRef(0);
    const [animated, setAnimated] = useState<Map<K, number>>(() => new Map());

    // Commit new samples during render so the RAF tick always sees fresh state.
    samples.forEach((v, k) => {
        const last = lastInputRef.current.get(k);
        if (last !== v) {
            const now = performance.now();
            const existing = stateRef.current.get(k);
            stateRef.current.set(k, {
                prevValue: existing?.curValue ?? v,
                curValue: v,
                curTs: now,
            });
            lastInputRef.current.set(k, v);
        }
    });

    // Remove keys that have disappeared.
    [...stateRef.current.keys()].forEach((k) => {
        if (!samples.has(k)) {
            stateRef.current.delete(k);
            lastInputRef.current.delete(k);
        }
    });

    useEffect(() => {
        let raf = 0;
        const tick = (): void => {
            const size = stateRef.current.size;

            // Skip setState entirely when there is nothing to animate and the
            // last published map was already empty — avoids per-frame churn
            // while the hook is idle (e.g. enabled:false, no keys loaded).
            if (size === 0 && lastPublishedSizeRef.current === 0) {
                raf = requestAnimationFrame(tick);
                return;
            }

            const now = performance.now();
            const next = new Map<K, number>();
            stateRef.current.forEach((s, k) => {
                const t = clampProgress(now, s.curTs, intervalMs);
                next.set(k, lerp(s.prevValue, s.curValue, t));
            });
            lastPublishedSizeRef.current = next.size;
            setAnimated(next);
            raf = requestAnimationFrame(tick);
        };
        raf = requestAnimationFrame(tick);
        return () => cancelAnimationFrame(raf);
    }, [intervalMs]);

    return animated;
};
