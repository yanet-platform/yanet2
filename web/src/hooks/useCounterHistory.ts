import { useMemo } from 'react';
import type { DeviceCounterData } from './useDeviceCounters';
import { useRollingWindow } from './useRollingWindow';

export const HISTORY_SIZE = 60;

export interface CounterHistoryEntry {
    rx: number[];
    tx: number[];
    rxBytes: number[];
    txBytes: number[];
}

/** Returns a new array with v appended, capped at cap elements. */
export const appendCapped = (arr: number[], v: number, cap: number): number[] =>
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
    const rxSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.rx.pps));
        return m;
    }, [counters]);

    const txSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.tx.pps));
        return m;
    }, [counters]);

    const rxBytesSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.rx.bps));
        return m;
    }, [counters]);

    const txBytesSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.tx.bps));
        return m;
    }, [counters]);

    const rxHistory = useRollingWindow(rxSamples, HISTORY_SIZE, 1000);
    const txHistory = useRollingWindow(txSamples, HISTORY_SIZE, 1000);
    const rxBytesHistory = useRollingWindow(rxBytesSamples, HISTORY_SIZE, 1000);
    const txBytesHistory = useRollingWindow(txBytesSamples, HISTORY_SIZE, 1000);

    return useMemo(() => {
        const result = new Map<string, CounterHistoryEntry>();
        counters.forEach((_, name) => {
            const rx = rxHistory.get(name);
            const tx = txHistory.get(name);
            const rxBytes = rxBytesHistory.get(name);
            const txBytes = txBytesHistory.get(name);
            if (rx && tx && rxBytes && txBytes) {
                result.set(name, { rx, tx, rxBytes, txBytes });
            }
        });
        return result;
    }, [counters, rxHistory, txHistory, rxBytesHistory, txBytesHistory]);
};
