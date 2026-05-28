import { useEffect, useRef, useState } from 'react';
import type { DeviceCounterData } from './useDeviceCounters';

const HISTORY_SIZE = 60;

export interface CounterHistoryEntry {
    rx: number[];
    tx: number[];
    rxBytes: number[];
    txBytes: number[];
}

/** Returns a new array with v appended, capped at cap elements. */
const appendCapped = (arr: number[], v: number, cap: number): number[] =>
    arr.length < cap ? [...arr, v] : [...arr.slice(1), v];

/**
 * Maintains a rolling 60-sample window (1 sample/sec) of counter history per device.
 *
 * Samples are taken from the provided counters map on a 1-second setInterval,
 * not on every interpolation frame. The returned map reference changes on each
 * sample so components that read it will re-render.
 *
 * Each tick produces fresh array references inside CounterHistoryEntry so that
 * React Compiler memoized children reliably detect the change via reference
 * equality on the entry prop.
 *
 * On first sight of a device the history is pre-seeded with HISTORY_SIZE copies
 * of the current value, giving an immediately-populated flat sparkline instead
 * of a single-sample spike that only goes down.
 */
export const useCounterHistory = (
    counters: Map<string, DeviceCounterData>
): Map<string, CounterHistoryEntry> => {
    // historyRef holds the mutable ring-buffer; we return a fresh Map snapshot on
    // each sample tick so React can detect the change.
    const historyRef = useRef<Map<string, CounterHistoryEntry>>(new Map());
    const [snapshot, setSnapshot] = useState<Map<string, CounterHistoryEntry>>(new Map());

    // Keep a stable ref to the latest counters map so the interval callback can
    // read it without being recreated every render.
    const countersRef = useRef(counters);
    countersRef.current = counters;

    useEffect(() => {
        const tick = () => {
            const current = countersRef.current;
            if (!current.size) return;

            const history = historyRef.current;
            let mutated = false;

            for (const [name, data] of current.entries()) {
                const entry = history.get(name);

                if (!entry) {
                    // Seed with HISTORY_SIZE copies of the current value so the
                    // sparkline starts as a flat line rather than a single-sample
                    // spike that immediately falls downward.
                    history.set(name, {
                        rx:      Array(HISTORY_SIZE).fill(data.rx.pps) as number[],
                        tx:      Array(HISTORY_SIZE).fill(data.tx.pps) as number[],
                        rxBytes: Array(HISTORY_SIZE).fill(data.rx.bps) as number[],
                        txBytes: Array(HISTORY_SIZE).fill(data.tx.bps) as number[],
                    });
                    mutated = true;
                    continue;
                }

                // Always produce fresh arrays and a fresh entry object so that
                // React Compiler memoized children see a changed reference.
                history.set(name, {
                    rx:      appendCapped(entry.rx,      data.rx.pps, HISTORY_SIZE),
                    tx:      appendCapped(entry.tx,      data.tx.pps, HISTORY_SIZE),
                    rxBytes: appendCapped(entry.rxBytes, data.rx.bps, HISTORY_SIZE),
                    txBytes: appendCapped(entry.txBytes, data.tx.bps, HISTORY_SIZE),
                });
                mutated = true;
            }

            if (mutated) {
                setSnapshot(new Map(history));
            }
        };

        // Fire immediately so any already-loaded counters are seeded at mount
        // without waiting the first full second.
        tick();
        const id = setInterval(tick, 1000);
        return () => clearInterval(id);
    }, []);

    return snapshot;
};
