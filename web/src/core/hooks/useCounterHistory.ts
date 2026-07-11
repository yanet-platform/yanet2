import { useMemo } from 'react';
import type { DeviceCounterData } from './useDeviceCounters';
import { useRollingWindow } from './useRollingWindow';

export const HISTORY_SIZE = 60;

export interface CounterHistoryEntry {
    inputRx: number[];
    inputTx: number[];
    inputDrop: number[];
    outputRx: number[];
    outputTx: number[];
    outputDrop: number[];
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
    // One rolling series per logical field; each samples the pps rate.
    const inputRxSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.inputRx.pps));
        return m;
    }, [counters]);

    const inputTxSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.inputTx.pps));
        return m;
    }, [counters]);

    const inputDropSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.inputDrop.pps));
        return m;
    }, [counters]);

    const outputRxSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.outputRx.pps));
        return m;
    }, [counters]);

    const outputTxSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.outputTx.pps));
        return m;
    }, [counters]);

    const outputDropSamples = useMemo(() => {
        const m = new Map<string, number>();
        counters.forEach((d, name) => m.set(name, d.outputDrop.pps));
        return m;
    }, [counters]);

    const inputRxHistory = useRollingWindow(inputRxSamples, HISTORY_SIZE, 1000);
    const inputTxHistory = useRollingWindow(inputTxSamples, HISTORY_SIZE, 1000);
    const inputDropHistory = useRollingWindow(inputDropSamples, HISTORY_SIZE, 1000);
    const outputRxHistory = useRollingWindow(outputRxSamples, HISTORY_SIZE, 1000);
    const outputTxHistory = useRollingWindow(outputTxSamples, HISTORY_SIZE, 1000);
    const outputDropHistory = useRollingWindow(outputDropSamples, HISTORY_SIZE, 1000);

    return useMemo(() => {
        const result = new Map<string, CounterHistoryEntry>();
        counters.forEach((_, name) => {
            const inputRx = inputRxHistory.get(name);
            const inputTx = inputTxHistory.get(name);
            const inputDrop = inputDropHistory.get(name);
            const outputRx = outputRxHistory.get(name);
            const outputTx = outputTxHistory.get(name);
            const outputDrop = outputDropHistory.get(name);
            if (inputRx && inputTx && inputDrop && outputRx && outputTx && outputDrop) {
                result.set(name, {
                    inputRx,
                    inputTx,
                    inputDrop,
                    outputRx,
                    outputTx,
                    outputDrop,
                });
            }
        });
        return result;
    }, [
        counters,
        inputRxHistory,
        inputTxHistory,
        inputDropHistory,
        outputRxHistory,
        outputTxHistory,
        outputDropHistory,
    ]);
};
