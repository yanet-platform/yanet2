import { useCallback, useRef, useState } from 'react';
import { useAsyncData } from '../../hooks/useAsyncData';
import { pdumpApi, type PdumpConfig, type PdumpRecord } from '../../api/pdump';
import { parsePacket, base64ToUint8Array } from '../../utils/packetParser';
import type { PdumpConfigInfo, CapturedPacket, CaptureState } from './types';

const MAX_PACKETS = 10000;

export const usePdumpConfigs = () => {
    const fetchConfigs = useCallback(async (): Promise<PdumpConfigInfo[]> => {
        const response = await pdumpApi.listConfigs();
        const configs = response.configs ?? [];

        // Fetch config details for each config
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

    return {
        configs: data ?? [],
        loading,
        error,
        refetch,
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

export const usePdumpCapture = () => {
    const [state, setState] = useState<CaptureState>({
        isCapturing: false,
        configName: null,
        packets: [],
        error: null,
    });

    const abortControllerRef = useRef<AbortController | null>(null);
    const packetIdRef = useRef(0);

    const startCapture = useCallback((configName: string) => {
        // Stop any existing capture
        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
        }

        const abortController = new AbortController();
        abortControllerRef.current = abortController;
        packetIdRef.current = 0;

        setState({
            isCapturing: true,
            configName,
            packets: [],
            error: null,
        });

        pdumpApi.readDump(
            configName,
            {
                onMessage: (record: PdumpRecord) => {
                    const packetData = record.data
                        ? base64ToUint8Array(record.data)
                        : new Uint8Array(0);
                    const parsed = parsePacket(packetData);

                    const capturedPacket: CapturedPacket = {
                        id: packetIdRef.current++,
                        timestamp: record.meta?.timestamp
                            ? new Date(Number(record.meta.timestamp) / 1000000) // Convert nanoseconds to milliseconds
                            : new Date(),
                        record,
                        parsed,
                    };

                    setState((prev) => {
                        const newPackets = [...prev.packets, capturedPacket];
                        // Keep only the last MAX_PACKETS packets
                        if (newPackets.length > MAX_PACKETS) {
                            newPackets.splice(0, newPackets.length - MAX_PACKETS);
                        }
                        return { ...prev, packets: newPackets };
                    });
                },
                onError: (error: Error) => {
                    setState((prev) => ({
                        ...prev,
                        isCapturing: false,
                        error,
                    }));
                },
                onEnd: () => {
                    setState((prev) => ({
                        ...prev,
                        isCapturing: false,
                    }));
                },
            },
            abortController.signal
        );
    }, []);

    const stopCapture = useCallback(() => {
        if (abortControllerRef.current) {
            abortControllerRef.current.abort();
            abortControllerRef.current = null;
        }
        setState((prev) => ({
            ...prev,
            isCapturing: false,
        }));
    }, []);

    const clearPackets = useCallback(() => {
        packetIdRef.current = 0;
        setState((prev) => ({
            ...prev,
            packets: [],
        }));
    }, []);

    return {
        ...state,
        startCapture,
        stopCapture,
        clearPackets,
    };
};

