import { useState, useEffect, useRef, useMemo } from 'react';

/**
 * Counter data with interpolated rate values.
 */
export interface InterpolatedCounterData {
    pps: number; // packets per second (interpolated)
    bps: number; // bytes per second (interpolated)
}

/**
 * Raw counter values at a point in time (cumulative).
 */
interface RawCounterSnapshot<K extends string = string> {
    timestamp: number;
    values: Map<K, { packets: bigint; bytes: bigint }>;
}

/**
 * Calculated rate at a point in time.
 */
interface RateSnapshot<K extends string = string> {
    timestamp: number;
    rates: Map<K, { pps: number; bps: number }>;
}

export interface UseInterpolatedCountersOptions<K extends string = string> {
    /**
     * List of keys to track counters for.
     */
    keys: K[];

    /**
     * Function to fetch raw counter values for all keys.
     * Should return a Map of key -> { packets, bytes } cumulative values.
     */
    fetchCounters: () => Promise<Map<K, { packets: bigint; bytes: bigint }>>;

    /**
     * Polling interval in milliseconds. Default: 1000ms.
     */
    pollingInterval?: number;

    /**
     * Interpolation update interval in milliseconds. Default: 30ms.
     */
    interpolationInterval?: number;

    /**
     * Whether to enable the hook. Default: true.
     */
    enabled?: boolean;
}

export interface UseInterpolatedCountersResult<K extends string = string> {
    /**
     * Map of key -> interpolated counter data.
     */
    counters: Map<K, InterpolatedCounterData>;
}

/**
 * Hook for fetching and interpolating counters.
 * 
 * - Polls counters at specified interval (default: 1 second)
 * - Calculates rate (pps/bps) from counter deltas
 * - Stores previous and current rate for interpolation
 * - Updates visual at specified interval (default: 30ms) using linear interpolation
 */
export const useInterpolatedCounters = <K extends string = string>(
    options: UseInterpolatedCountersOptions<K>
): UseInterpolatedCountersResult<K> => {
    const {
        keys,
        fetchCounters,
        pollingInterval = 1000,
        interpolationInterval = 30,
        enabled = true,
    } = options;

    // Interpolated counters for display
    const [counters, setCounters] = useState<Map<K, InterpolatedCounterData>>(new Map());

    // Store raw counter snapshots (cumulative values)
    const snapshotsRef = useRef<RawCounterSnapshot<K>[]>([]);

    // Store calculated rate snapshots for interpolation
    const ratesRef = useRef<RateSnapshot<K>[]>([]);

    // Track if component is mounted
    const isMountedRef = useRef(true);

    // Store keys ref to avoid effect re-triggers
    const keysRef = useRef<K[]>(keys);
    keysRef.current = keys;

    // Store fetch function ref
    const fetchCountersRef = useRef(fetchCounters);
    fetchCountersRef.current = fetchCounters;

    // Stable key for keys array to use in dependencies
    const keysKey = useMemo(() => JSON.stringify([...keys].sort()), [keys]);

    // Fetch counters at polling interval and calculate rates
    useEffect(() => {
        if (!enabled || keys.length === 0) return;

        const doFetch = async () => {
            if (!isMountedRef.current) return;

            const currentKeys = keysRef.current;

            try {
                const newValues = await fetchCountersRef.current();
                const now = Date.now();

                // Add new raw snapshot
                const newSnapshot: RawCounterSnapshot<K> = {
                    timestamp: now,
                    values: newValues,
                };

                // Calculate rate if we have a previous snapshot
                const prevSnapshot = snapshotsRef.current[snapshotsRef.current.length - 1];
                if (prevSnapshot) {
                    const timeDelta = (now - prevSnapshot.timestamp) / 1000; // seconds

                    if (timeDelta > 0) {
                        const newRates = new Map<K, { pps: number; bps: number }>();

                        for (const key of currentKeys) {
                            const prevVal = prevSnapshot.values.get(key);
                            const currVal = newValues.get(key);

                            if (prevVal && currVal) {
                                const packetsDelta = Number(currVal.packets - prevVal.packets);
                                const bytesDelta = Number(currVal.bytes - prevVal.bytes);

                                const pps = packetsDelta >= 0 ? packetsDelta / timeDelta : 0;
                                const bps = bytesDelta >= 0 ? bytesDelta / timeDelta : 0;

                                newRates.set(key, { pps, bps });
                            } else {
                                newRates.set(key, { pps: 0, bps: 0 });
                            }
                        }

                        // Add new rate snapshot
                        ratesRef.current.push({
                            timestamp: now,
                            rates: newRates,
                        });

                        // Keep only last 2 rate snapshots
                        if (ratesRef.current.length > 2) {
                            ratesRef.current.shift();
                        }
                    }
                }

                // Store raw snapshot
                snapshotsRef.current.push(newSnapshot);

                // Keep only last 2 raw snapshots
                if (snapshotsRef.current.length > 2) {
                    snapshotsRef.current.shift();
                }
            } catch (error) {
                console.error('Failed to fetch counters:', error);
            }
        };

        // Initial fetch
        doFetch();

        // Set up polling interval
        const intervalId = setInterval(doFetch, pollingInterval);

        return () => {
            clearInterval(intervalId);
        };
    }, [enabled, keysKey, pollingInterval]);

    // Interpolation timer - updates at interpolation interval
    useEffect(() => {
        if (!enabled) return;

        const interpolate = () => {
            if (!isMountedRef.current) return;

            const rates = ratesRef.current;
            const currentKeys = keysRef.current;

            // Need keys to display
            if (currentKeys.length === 0) return;

            // Need at least 1 rate snapshot
            if (rates.length === 0) return;

            const newCounters = new Map<K, InterpolatedCounterData>();
            const now = Date.now();

            if (rates.length === 1) {
                // Only one rate - just display it
                const rate = rates[0];
                for (const key of currentKeys) {
                    const r = rate.rates.get(key);
                    newCounters.set(key, { pps: r?.pps ?? 0, bps: r?.bps ?? 0 });
                }
            } else {
                // Two rates - interpolate between them
                const prevRate = rates[0];
                const currRate = rates[1];

                // Calculate progress: 0 at currRate.timestamp, 1 at currRate.timestamp + pollingInterval
                const elapsed = now - currRate.timestamp;
                const progress = Math.min(1, Math.max(0, elapsed / 1000));

                for (const key of currentKeys) {
                    const prev = prevRate.rates.get(key);
                    const curr = currRate.rates.get(key);

                    if (prev && curr) {
                        // Linear interpolation from prev rate to curr rate
                        const pps = prev.pps + (curr.pps - prev.pps) * progress;
                        const bps = prev.bps + (curr.bps - prev.bps) * progress;
                        newCounters.set(key, { pps, bps });
                    } else if (curr) {
                        newCounters.set(key, { pps: curr.pps, bps: curr.bps });
                    } else {
                        newCounters.set(key, { pps: 0, bps: 0 });
                    }
                }
            }

            setCounters(newCounters);
        };

        // Run interpolation at specified interval
        const intervalId = setInterval(interpolate, interpolationInterval);

        return () => {
            clearInterval(intervalId);
        };
    }, [enabled, interpolationInterval]);

    // Cleanup on unmount
    useEffect(() => {
        isMountedRef.current = true;
        return () => {
            isMountedRef.current = false;
        };
    }, []);

    return { counters };
};
