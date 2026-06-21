import { useEffect, useRef, useState } from 'react';

/**
 * Maintain a rolling history window for each key in `samples`.
 *
 * On each tick (every `intervalMs` ms) the current value is appended to the
 * per-key ring buffer, capped at `cap` entries. The returned Map and every
 * array inside it are fresh references each tick so React-Compiler memoized
 * children reliably detect the change via reference equality.
 *
 * Seed-on-first-sight: when a key appears for the first time it is pre-filled
 * with `cap` copies of its current value, producing an immediately flat
 * sparkline instead of a downward spike.
 *
 * An immediate first tick fires at mount so that already-loaded data is
 * seeded without waiting the first full interval.
 *
 * When `resetKey` changes the history is cleared so a consumer can drop stale
 * data across context switches (e.g. config change).
 */
export const useRollingWindow = <K>(
    samples: Map<K, number>,
    cap: number,
    intervalMs: number,
    resetKey?: string | number,
): Map<K, number[]> => {
    const samplesRef = useRef(samples);
    samplesRef.current = samples;

    const historyRef = useRef<Map<K, number[]>>(new Map());
    const [snapshot, setSnapshot] = useState<Map<K, number[]>>(() => new Map());

    // When resetKey changes, clear all accumulated history immediately.
    useEffect(() => {
        historyRef.current = new Map();
        setSnapshot(new Map());
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [resetKey]);

    useEffect(() => {
        const tick = (): void => {
            const current = samplesRef.current;
            const history = historyRef.current;
            let mutated = false;

            // Append/seed entries for keys present in the current sample set.
            current.forEach((v, k) => {
                const existing = history.get(k);
                if (existing === undefined) {
                    // Seed with cap copies so the sparkline starts as a flat line.
                    history.set(k, Array(cap).fill(v) as number[]);
                    mutated = true;
                } else {
                    const grown = [...existing, v];
                    if (grown.length > cap) grown.shift();
                    history.set(k, grown);
                    mutated = true;
                }
            });

            // Remove keys that have been dropped from samples. This runs even
            // when current is empty so stale history drains within one tick.
            [...history.keys()].forEach((k) => {
                if (!current.has(k)) {
                    history.delete(k);
                    mutated = true;
                }
            });

            if (mutated) {
                setSnapshot(new Map(history));
            }
        };

        tick();
        const id = setInterval(tick, intervalMs);
        return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [cap, intervalMs]);

    return snapshot;
};
