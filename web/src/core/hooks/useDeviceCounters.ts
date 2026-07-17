import { useCallback, useMemo } from 'react';
import { API } from '../api';
import { groupCounterPacketsAndBytes, makeGroupedCounterKey } from '../utils';
import {
    useInterpolatedCounters,
    type InterpolatedCounterData,
    type InterpolatedAbsoluteData,
} from './useInterpolatedCounters';

/**
 * Logical device counter fields.
 *
 * The dataplane exposes 12 device counters organised as rx/tx/drop per
 * input/output direction, plus `input_entry`. Each direction's counter has a
 * packets and a bytes variant; this union names the seven logical fields
 * surfaced to the UI (each carries both a rate and an absolute value).
 * `inputEntry` counts everything the device handler emitted into its input
 * pipelines, right after the handler and before dispatch — unlike
 * `inputTx`, it is not zeroed out when a packet leaves via the pending-output
 * list (e.g. a routed packet), which makes it the correct transmit-rate
 * source for generators.
 */
export type DeviceCounterField =
    | 'inputRx'
    | 'inputTx'
    | 'inputEntry'
    | 'inputDrop'
    | 'outputRx'
    | 'outputTx'
    | 'outputDrop';

/**
 * Maps each logical field to its backend size-2 counter name.
 *
 * Each backend counter is a size-2 vector where values[0] = packets and
 * values[1] = bytes. Backend naming convention: `<direction>_<kind>`, where
 * direction is `input`|`output` and kind is `rx`|`tx`|`entry`|`drop`.
 */
const DEVICE_COUNTER_FIELDS: ReadonlyArray<{
    field: DeviceCounterField;
    name: string;
}> = [
    { field: 'inputRx', name: 'input_rx' },
    { field: 'inputTx', name: 'input_tx' },
    { field: 'inputEntry', name: 'input_entry' },
    { field: 'inputDrop', name: 'input_drop' },
    { field: 'outputRx', name: 'output_rx' },
    { field: 'outputTx', name: 'output_tx' },
    { field: 'outputDrop', name: 'output_drop' },
];

/**
 * Device counter rate data (pps/bps) per logical field.
 */
export interface DeviceCounterData {
    inputRx: InterpolatedCounterData;
    inputTx: InterpolatedCounterData;
    inputEntry: InterpolatedCounterData;
    inputDrop: InterpolatedCounterData;
    outputRx: InterpolatedCounterData;
    outputTx: InterpolatedCounterData;
    outputDrop: InterpolatedCounterData;
}

/**
 * Device counter absolute data (cumulative packets/bytes) per logical field.
 */
export interface DeviceAbsoluteData {
    inputRx: InterpolatedAbsoluteData;
    inputTx: InterpolatedAbsoluteData;
    inputEntry: InterpolatedAbsoluteData;
    inputDrop: InterpolatedAbsoluteData;
    outputRx: InterpolatedAbsoluteData;
    outputTx: InterpolatedAbsoluteData;
    outputDrop: InterpolatedAbsoluteData;
}

export interface UseDeviceCountersResult {
    /**
     * Map of deviceName -> per-field rate data (pps/bps).
     * Returns undefined for a device if counters are still loading.
     */
    counters: Map<string, DeviceCounterData>;

    /**
     * Map of deviceName -> per-field absolute data (packets/bytes).
     * Returns undefined for a device if counters are still loading.
     */
    absoluteCounters: Map<string, DeviceAbsoluteData>;

    /**
     * Check if counters for a specific device are still loading.
     */
    isLoading: (deviceName: string) => boolean;
}

/** Build the interpolation key for a device + logical field. */
const counterKey = (deviceName: string, field: DeviceCounterField): string =>
    `${deviceName}:${field}`;

/**
 * Hook for fetching and interpolating device counters.
 *
 * Uses the generic useInterpolatedCounters hook with device-specific fetch logic.
 * - Polls counters every 1 second from backend
 * - Updates visual every 30ms using linear interpolation
 * - Returns per-field rates (pps/bps) for input/output rx/tx/drop per device
 * - Also returns interpolated absolute values for cumulative display
 *
 * @param deviceNames - Array of device names to track counters for
 * @param enabled - Whether to enable polling (default: true)
 */
export const useDeviceCounters = (
    deviceNames: string[],
    enabled: boolean = true
): UseDeviceCountersResult => {
    // Create keys for the interpolation hook: each device has one key per field.
    const keys = useMemo(() => {
        const result: string[] = [];
        for (const name of deviceNames) {
            for (const { field } of DEVICE_COUNTER_FIELDS) {
                result.push(counterKey(name, field));
            }
        }
        return result;
    }, [deviceNames]);

    // Fetch function that gets cumulative counter values for all devices.
    const fetchCounters = useCallback(async (): Promise<Map<string, { packets: bigint; bytes: bigint }>> => {
        const newValues = new Map<string, { packets: bigint; bytes: bigint }>();

        // Initialize with zeros.
        for (const name of deviceNames) {
            for (const { field } of DEVICE_COUNTER_FIELDS) {
                newValues.set(counterKey(name, field), { packets: BigInt(0), bytes: BigInt(0) });
            }
        }

        try {
            const response = await API.counters.byTags({
                tags: [
                    { key: 'device', value: '*' },
                    { key: 'pipeline', value: '' },
                ],
                query: DEVICE_COUNTER_FIELDS.map(({ name }) => name),
            });

            const grouped = groupCounterPacketsAndBytes(response.groups, ['device']);

            for (const deviceName of deviceNames) {
                for (const { field, name } of DEVICE_COUNTER_FIELDS) {
                    const value =
                        grouped.get(makeGroupedCounterKey([deviceName], name)) ?? {
                            packets: BigInt(0),
                            bytes: BigInt(0),
                        };
                    newValues.set(counterKey(deviceName, field), value);
                }
            }
        } catch {
            // Ignore global counters fetch errors.
        }

        return newValues;
    }, [deviceNames]);

    // Use the generic interpolated counters hook.
    const { counters: rawCounters, absoluteCounters: rawAbsoluteCounters } = useInterpolatedCounters({
        keys,
        fetchCounters,
        enabled: enabled && deviceNames.length > 0,
        pollingInterval: 1000,
        interpolationInterval: 30,
    });

    // Transform raw counters into DeviceCounterData map.
    const counters = useMemo(() => {
        const result = new Map<string, DeviceCounterData>();

        for (const deviceName of deviceNames) {
            const partial: Partial<DeviceCounterData> = {};
            let complete = true;
            for (const { field } of DEVICE_COUNTER_FIELDS) {
                const value = rawCounters.get(counterKey(deviceName, field));
                if (!value) {
                    complete = false;
                    break;
                }
                partial[field] = value;
            }
            // Only add if every field is available (not loading).
            if (complete) {
                result.set(deviceName, partial as DeviceCounterData);
            }
        }

        return result;
    }, [deviceNames, rawCounters]);

    // Transform raw absolute counters into DeviceAbsoluteData map.
    const absoluteCounters = useMemo(() => {
        const result = new Map<string, DeviceAbsoluteData>();

        for (const deviceName of deviceNames) {
            const partial: Partial<DeviceAbsoluteData> = {};
            let complete = true;
            for (const { field } of DEVICE_COUNTER_FIELDS) {
                const value = rawAbsoluteCounters.get(counterKey(deviceName, field));
                if (!value) {
                    complete = false;
                    break;
                }
                partial[field] = value;
            }
            if (complete) {
                result.set(deviceName, partial as DeviceAbsoluteData);
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
