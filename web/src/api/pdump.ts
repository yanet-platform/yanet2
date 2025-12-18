import { createService, createStreamingService, type StreamCallbacks } from './client';
import type { TargetModule } from './common';

// Pdump configuration modes (bitmap) - matches dataplane/mode.h
export const PDUMP_MODE = {
    INPUT: 1 << 0,  // 1
    DROP: 1 << 1,   // 2 (called PDUMP_DROPS in C)
} as const;

export interface PdumpConfig {
    filter?: string;
    mode?: number;
    snaplen?: number;
    ringSize?: number;
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowConfigResponse {
    config?: PdumpConfig;
}

export interface RecordMeta {
    timestamp?: string;
    dataSize?: number;
    packetLen?: number;
    workerIdx?: number;
    pipelineIdx?: number;
    rxDeviceId?: number;
    txDeviceId?: number;
    queue?: number;
}

export interface PdumpRecord {
    meta?: RecordMeta;
    data?: string; // base64 encoded
}

const pdumpService = createService('pdumppb.PdumpService');
const pdumpStreamService = createStreamingService('pdumppb.PdumpService');

export const pdumpApi = {
    listConfigs: (signal?: AbortSignal): Promise<ListConfigsResponse> => {
        return pdumpService.call<ListConfigsResponse>('ListConfigs', signal);
    },

    showConfig: (target: TargetModule, signal?: AbortSignal): Promise<ShowConfigResponse> => {
        return pdumpService.callWithBody<ShowConfigResponse>('ShowConfig', { target }, signal);
    },

    setConfig: (
        target: TargetModule,
        config: PdumpConfig,
        updateMask?: { paths?: string[] },
        signal?: AbortSignal
    ): Promise<void> => {
        return pdumpService.callWithBody<void>(
            'SetConfig',
            { target, config, updateMask },
            signal
        );
    },

    readDump: (
        target: TargetModule,
        callbacks: StreamCallbacks<PdumpRecord>,
        signal?: AbortSignal
    ): void => {
        pdumpStreamService.stream<PdumpRecord>('ReadDump', { target }, callbacks, signal);
    },
};

// Helper to parse mode bitmap to array of mode names
export const parseModeFlags = (mode: number): string[] => {
    const modes: string[] = [];
    if (mode & PDUMP_MODE.INPUT) modes.push('INPUT');
    if (mode & PDUMP_MODE.DROP) modes.push('DROP');
    return modes;
};

// Helper to convert mode array to bitmap
export const modeFlagsToNumber = (modes: string[]): number => {
    let result = 0;
    if (modes.includes('INPUT')) result |= PDUMP_MODE.INPUT;
    if (modes.includes('DROP')) result |= PDUMP_MODE.DROP;
    return result;
};
