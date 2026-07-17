import { describe, expect, it } from 'vitest';
import { displayRates } from './displayRates';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';

const makeCounterData = (overrides: Partial<Record<keyof DeviceCounterData, number>>): DeviceCounterData => {
    const zero = { pps: 0, bps: 0 };
    return {
        inputRx: { ...zero, pps: overrides.inputRx ?? 0 },
        inputTx: { ...zero, pps: overrides.inputTx ?? 0 },
        inputEntry: { ...zero, pps: overrides.inputEntry ?? 0 },
        inputDrop: { ...zero, pps: overrides.inputDrop ?? 0 },
        outputRx: { ...zero, pps: overrides.outputRx ?? 0 },
        outputTx: { ...zero, pps: overrides.outputTx ?? 0 },
        outputDrop: { ...zero, pps: overrides.outputDrop ?? 0 },
    } as DeviceCounterData;
};

const makeHistory = (overrides: Partial<CounterHistoryEntry>): CounterHistoryEntry => ({
    inputRx: [],
    inputTx: [],
    inputEntry: [],
    inputDrop: [],
    outputRx: [],
    outputTx: [],
    outputDrop: [],
    ...overrides,
});

describe('displayRates', () => {
    it('shows inputEntry for a traffic-generator device', () => {
        const counterData = makeCounterData({ inputEntry: 42, inputTx: 0, outputTx: 7 });
        const history = makeHistory({ inputEntry: [1, 2, 3], inputTx: [9, 9], outputTx: [7, 7] });

        const result = displayRates('trafgen', counterData, history);

        expect(result.txPps).toBe(42);
        expect(result.txHistory).toEqual([1, 2, 3]);
        expect(result.rxPps).toBe(0);
    });

    it('shows outputTx for a regular device', () => {
        const counterData = makeCounterData({ inputTx: 0, outputTx: 17 });
        const history = makeHistory({ inputTx: [], outputTx: [4, 5, 6] });

        const result = displayRates('vlan', counterData, history);

        expect(result.txPps).toBe(17);
        expect(result.txHistory).toEqual([4, 5, 6]);
    });

    it('shows outputTx for a plain device (trafficSource but not generator)', () => {
        const counterData = makeCounterData({ inputTx: 99, outputTx: 11 });
        const history = makeHistory({ inputTx: [9, 9, 9], outputTx: [1, 1] });

        const result = displayRates('plain', counterData, history);

        expect(result.txPps).toBe(11);
        expect(result.txHistory).toEqual([1, 1]);
    });

    it('falls back to outputTx for an unknown or undefined type', () => {
        const counterData = makeCounterData({ inputTx: 5, outputTx: 9 });
        const history = makeHistory({ outputTx: [7] });

        expect(displayRates('does-not-exist', counterData, history).txPps).toBe(9);
        expect(displayRates(undefined, counterData, history).txPps).toBe(9);
    });

    it('returns zeros when counter data and history are missing', () => {
        const result = displayRates('trafgen', undefined, undefined);

        expect(result).toEqual({ rxPps: 0, txPps: 0, rxHistory: [], txHistory: [] });
    });
});
