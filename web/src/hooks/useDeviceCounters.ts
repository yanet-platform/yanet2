import { useCallback, useMemo } from 'react';
import { API } from '../api';
import type { CounterInfo } from '../api';
import {
    useInterpolatedCounters,
    type InterpolatedCounterData,
    type InterpolatedAbsoluteData,
} from './useInterpolatedCounters';

/**
 * Device counter data with RX and TX rates.
 */
export interface DeviceCounterData {
    rx: InterpolatedCounterData;
    tx: InterpolatedCounterData;
}

/**
 * Device counter data with RX and TX absolute values.
 */
export interface DeviceAbsoluteData {
    rx: InterpolatedAbsoluteData;
    tx: InterpolatedAbsoluteData;
}

export interface UseDeviceCountersResult {
    /**
     * Map of deviceName -> { rx, tx } rate data (pps/bps).
     * Returns undefined for a device if counters are still loading.
     */
    counters: Map<string, DeviceCounterData>;

    /**
     * Map of deviceName -> { rx, tx } absolute data (packets/bytes).
     * Returns undefined for a device if counters are still loading.
     */
    absoluteCounters: Map<string, DeviceAbsoluteData>;

    /**
     * Check if counters for a specific device are still loading.
     */
    isLoading: (deviceName: string) => boolean;
}

/**
 * Helper to sum counter values across all instances.
 */
const sumCounterValues = (counter: CounterInfo | undefined): bigint => {
    if (!counter?.instances) return BigInt(0);
    return counter.instances.reduce((sum, inst) => {
        const val = inst.values?.[0];
        return sum + BigInt(val ?? 0);
    }, BigInt(0));
};

/**
 * Helper to find counter by name.
 */
const findCounter = (counters: CounterInfo[] | undefined, name: string): CounterInfo | undefined => {
    return counters?.find(c => c.name === name);
};

/**
 * Hook for fetching and interpolating device counters (RX/TX rates).
 *
 * Uses the generic useInterpolatedCounters hook with device-specific fetch logic.
 * - Polls counters every 1 second from backend
 * - Updates visual every 30ms using linear interpolation
 * - Returns both RX (rx, rx_bytes) and TX (tx, tx_bytes) rates per device
 * - Also returns interpolated absolute values for cumulative display
 *
 * @param deviceNames - Array of device names to track counters for
 * @param enabled - Whether to enable polling (default: true)
 */
export const useDeviceCounters = (
    deviceNames: string[],
    enabled: boolean = true
): UseDeviceCountersResult => {
    // Create keys for the interpolation hook: each device has rx and tx keys
    const keys = useMemo(() => {
        const result: string[] = [];
        for (const name of deviceNames) {
            result.push(`${name}:rx`, `${name}:tx`);
        }
        return result;
    }, [deviceNames]);

    // Fetch function that gets cumulative counter values for all devices
    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        // Initialize with zeros
        for (const name of deviceNames) {
            newValues.set(`${name}:rx`, { packets: BigInt(0), bytes: BigInt(0) });
            newValues.set(`${name}:tx`, { packets: BigInt(0), bytes: BigInt(0) });
        }

        // Fetch counters for each device
        await Promise.all(
            deviceNames.map(async (deviceName) => {
                try {
                    const response = await API.counters.device({ device: deviceName });

                    const rxPackets = sumCounterValues(findCounter(response.counters, 'rx'));
                    const rxBytes = sumCounterValues(findCounter(response.counters, 'rx_bytes'));
                    const txPackets = sumCounterValues(findCounter(response.counters, 'tx'));
                    const txBytes = sumCounterValues(findCounter(response.counters, 'tx_bytes'));

                    newValues.set(`${deviceName}:rx`, { packets: rxPackets, bytes: rxBytes });
                    newValues.set(`${deviceName}:tx`, { packets: txPackets, bytes: txBytes });
                } catch {
                    // Ignore errors for individual device counters
                }
            })
        );

        return newValues;
    }, [deviceNames]);

    // Use the generic interpolated counters hook
    const { counters: rawCounters, absoluteCounters: rawAbsoluteCounters } = useInterpolatedCounters({
        keys,
        fetchCounters,
        enabled: enabled && deviceNames.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    // Transform raw counters into DeviceCounterData map
    const counters = useMemo(() => {
        const result = new Map<string, DeviceCounterData>();

        for (const deviceName of deviceNames) {
            const rx = rawCounters.get(`${deviceName}:rx`);
            const tx = rawCounters.get(`${deviceName}:tx`);

            // Only add if both rx and tx are available (not loading)
            if (rx && tx) {
                result.set(deviceName, { rx, tx });
            }
        }

        return result;
    }, [deviceNames, rawCounters]);

    // Transform raw absolute counters into DeviceAbsoluteData map
    const absoluteCounters = useMemo(() => {
        const result = new Map<string, DeviceAbsoluteData>();

        for (const deviceName of deviceNames) {
            const rx = rawAbsoluteCounters.get(`${deviceName}:rx`);
            const tx = rawAbsoluteCounters.get(`${deviceName}:tx`);

            if (rx && tx) {
                result.set(deviceName, { rx, tx });
            }
        }

        return result;
    }, [deviceNames, rawAbsoluteCounters]);

    const isLoading = useCallback(
        (deviceName: string): boolean => {
            return !counters.has(deviceName);
        },
        [counters]
    );

    return { counters, absoluteCounters, isLoading };
};
