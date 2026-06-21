import { useState, useEffect, useRef, useMemo } from 'react';
import { computeRate } from '../utils/interpolation';
import { useLagInterpolated } from './useLagInterpolated';

/**
 * Counter data with interpolated rate values.
 */
export interface InterpolatedCounterData {
    pps: number; // packets per second (interpolated)
    bps: number; // bytes per second (interpolated)
}

/**
 * Counter data with interpolated absolute values.
 */
export interface InterpolatedAbsoluteData {
    packets: number; // interpolated packet count
    bytes: number; // interpolated byte count
}

/**
 * Raw counter values at a point in time (cumulative).
 */
interface RawCounterSnapshot<K extends string = string> {
    timestamp: number;
    values: Map<K, { packets: bigint; bytes: bigint }>;
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
     *
     * This option is accepted for backwards compatibility but is ignored
     * internally — interpolation now runs via requestAnimationFrame.
     */
    interpolationInterval?: number;

    /**
     * Whether to enable the hook. Default: true.
     */
    enabled?: boolean;
}

export interface UseInterpolatedCountersResult<K extends string = string> {
    /**
     * Map of key -> interpolated rate data (pps/bps).
     */
    counters: Map<K, InterpolatedCounterData>;

    /**
     * Map of key -> interpolated absolute data (packets/bytes).
     * Interpolates cumulative packet/byte counts between the two most recent
     * raw snapshots; never extrapolates past the current sample.
     */
    absoluteCounters: Map<K, InterpolatedAbsoluteData>;
}

/**
 * Hook for fetching and interpolating counters.
 *
 * - Polls counters at the specified interval (default: 1 second).
 * - Computes per-second rates from counter deltas via computeRate.
 * - Smoothly interpolates pps/bps and absolute packet/byte counts between
 *   poll snapshots using RAF lag-interpolation (prev -> cur, clamp to [0,1]).
 */
export const useInterpolatedCounters = <K extends string = string>(
    options: UseInterpolatedCountersOptions<K>
): UseInterpolatedCountersResult<K> => {
    const {
        keys,
        fetchCounters,
        pollingInterval = 1000,
        enabled = true,
    } = options;

    // Latest two raw cumulative snapshots for rate and absolute interpolation.
    const snapshotsRef = useRef<RawCounterSnapshot<K>[]>([]);

    // pps samples fed into useLagInterpolated — one map entry per key.
    const [ppsSamples, setPpsSamples] = useState<Map<K, number>>(() => new Map());
    // bps samples fed into useLagInterpolated.
    const [bpsSamples, setBpsSamples] = useState<Map<K, number>>(() => new Map());

    // Mirror refs so doFetch can read the latest published maps without
    // capturing stale closure values (the effect deps list only needs keysKey,
    // not these maps).
    const ppsSamplesRef = useRef<Map<K, number>>(ppsSamples);
    const bpsSamplesRef = useRef<Map<K, number>>(bpsSamples);
    ppsSamplesRef.current = ppsSamples;
    bpsSamplesRef.current = bpsSamples;
    // Absolute packet samples (cumulative BigInt -> Number snapshot).
    const [packetSamples, setPacketSamples] = useState<Map<K, number>>(() => new Map());
    // Absolute byte samples.
    const [bytesSamples, setBytesSamples] = useState<Map<K, number>>(() => new Map());

    const keysRef = useRef<K[]>(keys);
    keysRef.current = keys;

    const fetchCountersRef = useRef(fetchCounters);
    fetchCountersRef.current = fetchCounters;

    const keysKey = useMemo(() => JSON.stringify([...keys].sort()), [keys]);

    // Clear stale snapshots when the key set changes to avoid garbage deltas
    // across config switches.
    useEffect(() => {
        snapshotsRef.current = [];
        const emptyPps = new Map<K, number>();
        const emptyBps = new Map<K, number>();
        ppsSamplesRef.current = emptyPps;
        bpsSamplesRef.current = emptyBps;
        setPpsSamples(emptyPps);
        setBpsSamples(emptyBps);
        setPacketSamples(new Map());
        setBytesSamples(new Map());
    }, [keysKey]);

    // Poll loop: fetch cumulative counters, compute rates, publish samples.
    useEffect(() => {
        if (!enabled || keys.length === 0) return;

        let cancelled = false;
        let timerId: ReturnType<typeof setTimeout> | undefined;

        const doFetch = async (): Promise<void> => {
            if (cancelled) return;

            const currentKeys = keysRef.current;

            try {
                const newValues = await fetchCountersRef.current();
                if (cancelled) return;

                const now = performance.now();
                const newSnapshot: RawCounterSnapshot<K> = {
                    timestamp: now,
                    values: newValues,
                };

                const prevSnapshot = snapshotsRef.current[snapshotsRef.current.length - 1];

                if (prevSnapshot) {
                    const dtSeconds = (now - prevSnapshot.timestamp) / 1000;

                    let updatedPps: Map<K, number> | null = null;
                    let updatedBps: Map<K, number> | null = null;

                    for (const key of currentKeys) {
                        const prev = prevSnapshot.values.get(key);
                        const cur = newValues.get(key);

                        if (prev && cur) {
                            const rate = computeRate(prev, cur, dtSeconds);
                            if (rate !== null) {
                                if (updatedPps === null) {
                                    updatedPps = new Map<K, number>(ppsSamplesRef.current);
                                    updatedBps = new Map<K, number>(bpsSamplesRef.current);
                                }
                                updatedPps.set(key, rate.pps);
                                updatedBps!.set(key, rate.bps);
                            }
                        }
                    }

                    if (updatedPps !== null) {
                        setPpsSamples(updatedPps);
                        setBpsSamples(updatedBps!);
                    }
                }

                // Publish absolute samples from the latest snapshot.
                const nextPackets = new Map<K, number>();
                const nextBytes = new Map<K, number>();
                for (const key of currentKeys) {
                    const cur = newValues.get(key);
                    nextPackets.set(key, cur ? Number(cur.packets) : 0);
                    nextBytes.set(key, cur ? Number(cur.bytes) : 0);
                }
                setPacketSamples(nextPackets);
                setBytesSamples(nextBytes);

                snapshotsRef.current.push(newSnapshot);
                if (snapshotsRef.current.length > 2) {
                    snapshotsRef.current.shift();
                }
            } catch {
                // tolerate fetch failures.
            } finally {
                if (!cancelled) {
                    timerId = setTimeout(() => { void doFetch(); }, pollingInterval);
                }
            }
        };

        void doFetch();
        return () => {
            cancelled = true;
            clearTimeout(timerId);
        };
    }, [enabled, keysKey, pollingInterval]);

    // Lag-interpolate pps, bps, packets, bytes independently.
    const interpPps = useLagInterpolated(ppsSamples, pollingInterval);
    const interpBps = useLagInterpolated(bpsSamples, pollingInterval);
    const interpPackets = useLagInterpolated(packetSamples, pollingInterval);
    const interpBytes = useLagInterpolated(bytesSamples, pollingInterval);

    // Assemble output maps. Build fresh Map objects each render so consumers
    // that depend on map identity detect the change.
    const counters = useMemo((): Map<K, InterpolatedCounterData> => {
        const out = new Map<K, InterpolatedCounterData>();
        for (const key of keys) {
            const pps = interpPps.get(key);
            const bps = interpBps.get(key);
            if (pps !== undefined && bps !== undefined) {
                out.set(key, { pps, bps });
            }
        }
        return out;
    }, [keys, interpPps, interpBps]);

    const absoluteCounters = useMemo((): Map<K, InterpolatedAbsoluteData> => {
        const out = new Map<K, InterpolatedAbsoluteData>();
        for (const key of keys) {
            const packets = interpPackets.get(key);
            const bytes = interpBytes.get(key);
            if (packets !== undefined && bytes !== undefined) {
                out.set(key, { packets, bytes });
            }
        }
        return out;
    }, [keys, interpPackets, interpBytes]);

    return { counters, absoluteCounters };
};
