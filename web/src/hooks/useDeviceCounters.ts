import { useCallback, useMemo } from 'react';
import { API } from '../api';
import { groupCounterGroupsByTagsAndName, makeGroupedCounterKey } from '../utils';
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

        try {
            const response = await API.counters.byTags({
                tags: [
                    { key: 'device', value: '*' },
                    { key: 'pipeline', value: '' },
                ],
                query: ['rx', 'rx_bytes', 'tx', 'tx_bytes'],
            });

            const grouped = groupCounterGroupsByTagsAndName(response.groups, ['device'], 0);

            for (const deviceName of deviceNames) {
                const rxPackets = grouped.get(makeGroupedCounterKey([deviceName], 'rx'))?.value ?? BigInt(0);
                const rxBytes = grouped.get(makeGroupedCounterKey([deviceName], 'rx_bytes'))?.value ?? BigInt(0);
                const txPackets = grouped.get(makeGroupedCounterKey([deviceName], 'tx'))?.value ?? BigInt(0);
                const txBytes = grouped.get(makeGroupedCounterKey([deviceName], 'tx_bytes'))?.value ?? BigInt(0);

                newValues.set(`${deviceName}:rx`, { packets: rxPackets, bytes: rxBytes });
                newValues.set(`${deviceName}:tx`, { packets: txPackets, bytes: txBytes });
            }
        } catch {
            // Ignore global counters fetch errors.
        }

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
