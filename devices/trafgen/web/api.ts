import { createService, type CallOptions } from '@yanet/core/api/client';
import type { Device } from '@yanet/core/api/devices';

export interface TrafgenListConfigsResponse {
    configs?: string[];
}

export interface TrafgenUpdateDeviceRequest {
    name: string;
    /** Input/output pipeline assignments the generated traffic flows into. */
    device: Device;
}

export interface TrafgenShowConfigResponse {
    /** Target aggregate rate in packets per second (arrives as string — uint64 on the wire). */
    rate_pps?: string;
    frame_count?: number;
    /** Total bytes of the loaded pcap frames (arrives as string — uint64 on the wire). */
    total_bytes?: string;
}

export interface TrafgenShowPacketsResponse {
    /** Each entry is a base64-encoded raw L2 frame. */
    packets?: string[];
    /** True when the server returned a capped subset of the available frames. */
    truncated?: boolean;
}

export interface TrafgenUploadPcapRequest {
    name: string;
    /** Base64-encoded raw .pcap file bytes. */
    pcap: string;
}

export interface TrafgenSetRateRequest {
    name: string;
    /** Target aggregate rate in packets per second. */
    rate_pps: number;
}

const trafgenService = createService('devices.trafgen.controlplane.trafgenpb.v1.TrafgenService');

export const trafgen = {
    updateDevice: (request: TrafgenUpdateDeviceRequest, options?: CallOptions): Promise<Record<string, never>> =>
        trafgenService.callWithBody<Record<string, never>>('UpdateDevice', request, options),

    listConfigs: (options?: CallOptions): Promise<TrafgenListConfigsResponse> =>
        trafgenService.call<TrafgenListConfigsResponse>('ListConfigs', options),

    showConfig: (name: string, options?: CallOptions): Promise<TrafgenShowConfigResponse> =>
        trafgenService.callWithBody<TrafgenShowConfigResponse>('ShowConfig', { name }, options),

    showPackets: (name: string, options?: CallOptions): Promise<TrafgenShowPacketsResponse> =>
        trafgenService.callWithBody<TrafgenShowPacketsResponse>('ShowPackets', { name }, options),

    uploadPcap: (request: TrafgenUploadPcapRequest, options?: CallOptions): Promise<Record<string, never>> =>
        trafgenService.callWithBody<Record<string, never>>('UploadPcap', request, options),

    setRate: (request: TrafgenSetRateRequest, options?: CallOptions): Promise<Record<string, never>> =>
        trafgenService.callWithBody<Record<string, never>>('SetRate', request, options),
};
