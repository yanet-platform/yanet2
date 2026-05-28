import { useCallback, useRef, useState, useEffect } from 'react';
import { useAsyncData } from '../../../hooks/useAsyncData';
import { pdumpApi, type PdumpConfig, type PdumpRecord } from '../../../api/pdump';
import { base64ToUint8Array, parsePacket, toaster } from '../../../utils';
import type { PdumpConfigInfo, CapturedPacket } from './types';

const MAX_PACKETS = 5000;
const MAX_BUFFER_BYTES = 64 * 1024 * 1024; // 64 MiB
const PPS_WINDOW_COUNT = 30;
const PPS_SAMPLE_INTERVAL_MS = 1000;
const FLUSH_INTERVAL_MS = 500;

export const usePdumpConfigs = () => {
    const fetchConfigs = useCallback(async (): Promise<PdumpConfigInfo[]> => {
        const response = await pdumpApi.listConfigs();
        const configs = response.configs ?? [];

        const configInfos = await Promise.all(
            configs.map(async (name) => {
                try {
                    const configResponse = await pdumpApi.showConfig(name);
                    return {
                        name,
                        config: configResponse.config,
                    };
                } catch {
                    return { name };
                }
            })
        );

        return configInfos;
    }, []);

    const { data, loading, error, refetch } = useAsyncData<PdumpConfigInfo[]>({
        fetchFn: fetchConfigs,
        errorToastName: 'pdump-configs-error',
        errorMessage: 'Failed to load pdump configs',
    });

    const deleteConfig = useCallback(async (configName: string): Promise<boolean> => {
        try {
            await pdumpApi.deleteConfig(configName);
            toaster.success('pdump-delete-success', `Config ${configName} deleted successfully`);
            refetch();
            return true;
        } catch (err) {
            toaster.error('pdump-delete-error', `Failed to delete config ${configName}`, err);
            return false;
        }
    }, [refetch]);

    return {
        configs: data ?? [],
        loading,
        error,
        refetch,
        deleteConfig,
    };
};

export const usePdumpConfig = (configName: string) => {
    const updateConfig = useCallback(
        async (config: PdumpConfig) => {
            await pdumpApi.setConfig(configName, config);
        },
        [configName]
    );

    return { updateConfig };
};

interface CaptureState {
    /** Config currently being streamed, or null. */
    liveConfig: string | null;
    /** Per-config packet buffers. */
    packetsByConfig: Record<string, CapturedPacket[]>;
    error: Error | null;
}

/** Applies the packet-count and byte-cap trim to a combined packet array. */
function trimPackets(combined: CapturedPacket[]): CapturedPacket[] {
    let result = combined;
    if (result.length > MAX_PACKETS) {
        result = result.slice(result.length - MAX_PACKETS);
    }
    let totalBytes = result.reduce((acc, p) => acc + p.parsed.raw.length, 0);
    let trimStart = 0;
    while (totalBytes > MAX_BUFFER_BYTES && trimStart < result.length) {
        const p = result[trimStart];
        if (p) {
            totalBytes -= p.parsed.raw.length;
        }
        trimStart++;
    }
    if (trimStart > 0) {
        result = result.slice(trimStart);
    }
    return result;
}

export const usePdumpCapture = (paused: boolean) => {
    const pausedRef = useRef(paused);
    pausedRef.current = paused;
    const [state, setState] = useState<CaptureState>({
        liveConfig: null,
        packetsByConfig: {},
        error: null,
    });

    const abortControllerRef = useRef<AbortController | null>(null);
    const packetIdRef = useRef(0);
    const bufferRef = useRef<{ configName: string; packets: CapturedPacket[] }>({
        configName: '',
        packets: [],
    });
    const intervalRef = useRef<number | null>(null);

    const arrivalsCounterRef = useRef<Record<string, number>>({});
    const prevCountersRef = useRef<Record<string, number>>({});
    const ppsSamplerRef = useRef<number | null>(null);
    const [ppsByConfig, setPpsByConfig] = useState<Record<string, number[]>>({});

    /** Flushes the current buffer into state. No-ops while paused. */
    const flushBuffer = useCallback(() => {
        if (pausedRef.current) return;
        const incoming = bufferRef.current;
        if (incoming.packets.length === 0) {
            return;
        }
        const incomingConfigName = incoming.configName;
        const incomingPackets = incoming.packets;
        bufferRef.current = { configName: incomingConfigName, packets: [] };

        setState((prev) => {
            const existing = prev.packetsByConfig[incomingConfigName] ?? [];
            const combined = trimPackets([...existing, ...incomingPackets]);
            return {
                ...prev,
                packetsByConfig: {
                    ...prev.packetsByConfig,
                    [incomingConfigName]: combined,
                },
            };
        });
    }, []);

    const startCapture = useCallback((configName: string) => {
        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
        }

        // Clear any running timer and synchronously flush pending packets
        // for the previous config before switching to the new config name.
        if (intervalRef.current !== null) {
            clearTimeout(intervalRef.current);
            intervalRef.current = null;
            flushBuffer();
        }

        const abortController = new AbortController();
        abortControllerRef.current = abortController;

        bufferRef.current = { configName, packets: [] };

        setState((prev) => ({
            ...prev,
            liveConfig: configName,
            error: null,
        }));

        const tick = () => {
            flushBuffer();
            if (intervalRef.current !== null) {
                intervalRef.current = window.setTimeout(tick, FLUSH_INTERVAL_MS);
            }
        };
        intervalRef.current = window.setTimeout(tick, FLUSH_INTERVAL_MS);

        const samplePps = () => {
            // Phase 1: compute deltas and advance prevCountersRef exactly once
            // per tick, outside the React updater. The updater may be called
            // multiple times by React (StrictMode / concurrent mode), which
            // would otherwise advance the ref on each invocation and produce
            // zero deltas on the second call — causing sparkline aliasing.
            const counter = arrivalsCounterRef.current;
            const prevCounters = prevCountersRef.current;
            const deltas: Record<string, number> = {};
            const nextPrev: Record<string, number> = { ...prevCounters };
            for (const cfg of Object.keys(counter)) {
                const total = counter[cfg] ?? 0;
                const prevTotal = prevCounters[cfg] ?? 0;
                deltas[cfg] = total - prevTotal;
                nextPrev[cfg] = total;
            }
            prevCountersRef.current = nextPrev;

            // Phase 2: pure state updater — reads the precomputed deltas only,
            // no side effects, safe for multiple invocations.
            setPpsByConfig(prev => {
                const next = { ...prev };
                for (const cfg of Object.keys(deltas)) {
                    const history = next[cfg] ?? new Array<number>(PPS_WINDOW_COUNT).fill(0);
                    next[cfg] = [...history.slice(1), deltas[cfg] ?? 0];
                }
                return next;
            });

            ppsSamplerRef.current = window.setTimeout(samplePps, PPS_SAMPLE_INTERVAL_MS);
        };
        if (ppsSamplerRef.current !== null) {
            clearTimeout(ppsSamplerRef.current);
        }
        ppsSamplerRef.current = window.setTimeout(samplePps, PPS_SAMPLE_INTERVAL_MS);

        pdumpApi.readDump(
            configName,
            {
                onMessage: (record: PdumpRecord) => {
                    if (pausedRef.current) return;
                    const packetData = record.data
                        ? base64ToUint8Array(record.data)
                        : new Uint8Array(0);
                    const parsed = parsePacket(packetData);

                    // Strip record.data (the raw base64 string) from the stored record:
                    // PacketDrawer reads only record.meta.* fields; all byte-level display
                    // uses parsed.raw (Uint8Array). At snaplen=16384 the base64 string is
                    // ~22 KB; dropping it saves ~110 MB across MAX_PACKETS=5000 packets.
                    const slimRecord: PdumpRecord = record.data
                        ? { ...record, data: undefined }
                        : record;

                    const capturedPacket: CapturedPacket = {
                        id: packetIdRef.current++,
                        timestamp: record.meta?.timestamp
                            ? new Date(Number(record.meta.timestamp) / 1000000)
                            : new Date(),
                        record: slimRecord,
                        parsed,
                    };

                    // Hot path: in-place push is safe because bufferRef is consumed at
                    // flush time by flushBuffer (which replaces it with a fresh empty
                    // object). React never observes this ref between flushes. Avoids
                    // O(N) spread allocation per packet at sustained high pps.
                    const buf = bufferRef.current;
                    if (buf.packets.length >= MAX_PACKETS) {
                        buf.packets.shift();
                    }
                    buf.packets.push(capturedPacket);

                    const cfg = bufferRef.current.configName;
                    // Hot path: in-place mutation is safe because arrivalsCounterRef
                    // is read only by samplePps (1 Hz) and never compared by reference
                    // to trigger React re-renders. Avoids a new object allocation per packet.
                    const counter = arrivalsCounterRef.current;
                    counter[cfg] = (counter[cfg] ?? 0) + 1;
                },
                onError: (error: Error) => {
                    setState((prev) => ({
                        ...prev,
                        liveConfig: null,
                        error,
                    }));
                },
                onEnd: () => {
                    setState((prev) => ({
                        ...prev,
                        liveConfig: null,
                    }));
                },
            },
            abortController.signal
        );
    }, [flushBuffer]);

    const stopCapture = useCallback(() => {
        if (intervalRef.current !== null) {
            // Null first so any in-flight tick callback skips rescheduling.
            const handle = intervalRef.current;
            intervalRef.current = null;
            clearTimeout(handle);
        }
        if (ppsSamplerRef.current !== null) {
            clearTimeout(ppsSamplerRef.current);
            ppsSamplerRef.current = null;
        }
        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
            abortControllerRef.current = null;
        }
        // Any packets that arrived after the last flush tick but before the
        // abort completes remain in bufferRef and are silently dropped.
        bufferRef.current = { configName: '', packets: [] };
        setState((prev) => ({
            ...prev,
            liveConfig: null,
        }));
    }, []);

    const clearPackets = useCallback((configName: string) => {
        packetIdRef.current = 0;
        bufferRef.current = { configName, packets: [] };
        arrivalsCounterRef.current = { ...arrivalsCounterRef.current, [configName]: 0 };
        prevCountersRef.current = { ...prevCountersRef.current, [configName]: 0 };
        setState((prev) => ({
            ...prev,
            packetsByConfig: {
                ...prev.packetsByConfig,
                [configName]: [],
            },
        }));
    }, []);

    useEffect(() => {
        return () => {
            if (intervalRef.current !== null) {
                clearTimeout(intervalRef.current);
            }
            if (ppsSamplerRef.current !== null) {
                clearTimeout(ppsSamplerRef.current);
            }
            abortControllerRef.current?.abort();
        };
    }, []);

    return {
        liveConfig: state.liveConfig,
        packetsByConfig: state.packetsByConfig,
        ppsByConfig,
        error: state.error,
        isCapturing: state.liveConfig !== null,
        startCapture,
        stopCapture,
        clearPackets,
    };
};

/** Stable empty packet array for configs with no captured packets yet. */
const EMPTY_PACKETS: CapturedPacket[] = [];

/**
 * Selects the packets for a single config from the per-config map,
 * returning a stable empty array when there are none.
 */
export const useConfigPackets = (
    packetsByConfig: Record<string, CapturedPacket[]>,
    configName: string
): CapturedPacket[] => {
    return packetsByConfig[configName] ?? EMPTY_PACKETS;
};
