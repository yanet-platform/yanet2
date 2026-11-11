import { createService, createStreamingService, type CallOptions, type StreamCallbacks } from './client';

export const PDUMP_RATE_PPS_MAX = 1_000_000;

// Pdump configuration modes (bitmap) - matches dataplane/mode.h
export const PDUMP_MODE = {
    INPUT: 1 << 0,  // 1
    DROP: 1 << 1,   // 2 (called PDUMP_DROPS in C)
} as const;

export interface PdumpConfig {
    filter?: string;
    mode?: number;
    snaplen?: number;
    // Name of the ring object this config captures into.
    //
    // Required on create; absent on an update keeps the bound ring, an
    // explicit empty string is rejected.
    ring_name?: string;
    rate_pps?: number;
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowConfigResponse {
    config?: PdumpConfig;
}

export interface RecordMeta {
    timestamp?: number | string;
    data_size?: number;
    packet_len?: number;
    worker_idx?: number;
    pipeline_idx?: number;
    // Legacy alias of device_id, kept during the field rename so a UI
    // newer than the control plane keeps decoding older servers.
    tx_device_id?: number;
    device_id?: number;
    queue?: number;
}

export interface PdumpRecord {
    meta?: RecordMeta;
    data?: string; // base64 encoded
}

const pdumpService = createService('modules.pdump.controlplane.pdumppb.v1.PdumpService');
const pdumpStreamService = createStreamingService('modules.pdump.controlplane.pdumppb.v1.PdumpService');

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
        options?: CallOptions
    ): Promise<void> => {
        if (config.rate_pps !== undefined && (!Number.isInteger(config.rate_pps) || config.rate_pps < 0 || config.rate_pps > PDUMP_RATE_PPS_MAX)) {
            return Promise.reject(new Error(`Rate must be an integer between 0 and ${PDUMP_RATE_PPS_MAX}`));
        }
        return pdumpService.callWithBody<void>(
            'SetConfig',
            { name, config },
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
