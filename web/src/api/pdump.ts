import { createService, createStreamingService, type CallOptions, type StreamCallbacks } from './client';

// Pdump configuration modes (bitmap) - matches dataplane/mode.h
export const PDUMP_MODE = {
    INPUT: 1 << 0,  // 1
    DROP: 1 << 1,   // 2 (called PDUMP_DROPS in C)
} as const;

export interface PdumpConfig {
    filter?: string;
    mode?: number;
    snaplen?: number;
    ring_size?: number;
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowConfigResponse {
    config?: PdumpConfig;
}

export interface RecordMeta {
    timestamp?: string;
    data_size?: number;
    packet_len?: number;
    worker_idx?: number;
    pipeline_idx?: number;
    rx_device_id?: number;
    tx_device_id?: number;
    queue?: number;
}

export interface PdumpRecord {
    meta?: RecordMeta;
    data?: string; // base64 encoded
}

export interface FieldMask {
    paths?: string[];
}

const pdumpService = createService('pdumppb.PdumpService');
const pdumpStreamService = createStreamingService('pdumppb.PdumpService');

export const pdumpApi = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> => {
        return pdumpService.call<ListConfigsResponse>('ListConfigs', options);
    },

    showConfig: (name: string, options?: CallOptions): Promise<ShowConfigResponse> => {
        return pdumpService.callWithBody<ShowConfigResponse>('ShowConfig', { name }, options);
    },

    setConfig: (
        name: string,
        config: PdumpConfig,
        update_mask?: FieldMask,
        options?: CallOptions
    ): Promise<void> => {
        return pdumpService.callWithBody<void>(
            'SetConfig',
            { name, config, update_mask },
            options
        );
    },

    readDump: (
        name: string,
        callbacks: StreamCallbacks<PdumpRecord>,
        signal?: AbortSignal
    ): void => {
        pdumpStreamService.stream<PdumpRecord>('ReadDump', { name }, callbacks, signal);
    },

    deleteConfig: (name: string, options?: CallOptions): Promise<void> => {
        return pdumpService.callWithBody<void>('DeleteConfig', { name }, options);
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
