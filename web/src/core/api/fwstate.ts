import { createService, createStreamingService, type CallOptions, type StreamCallbacks } from './client';
import type { MACAddress } from './neighbours';
import type { IPAddressWire } from '../utils/netip';

export interface SyncConfig {
    src_addr?: IPAddressWire;
    dst_ether?: MACAddress;
    dst_addr_multicast?: IPAddressWire;
    port_multicast?: number;
    dst_addr_unicast?: IPAddressWire;
    port_unicast?: number;
    tcp_syn_ack?: number;
    tcp_syn?: number;
    tcp_fin?: number;
    tcp?: number;
    udp?: number;
    default?: number;
}

export interface ShowConfigResponse {
    name?: string;
    sync_config?: SyncConfig;
    // Name of the standalone fwstate-map this config references.
    map_name?: string;
}

import type { ListConfigsResponse } from './shared';
export type { ListConfigsResponse };

export interface MapStats {
    index_size?: number;
    extra_bucket_count?: number;
    max_chain_length?: number;
    layer_count?: number;
    total_elements?: number;
    max_deadline?: number;
    memory_used?: number;
    note?: string;
}

export interface GetStatsResponse {
    ipv4_stats?: MapStats;
    ipv6_stats?: MapStats;
}

export enum Direction {
    FORWARD = 0,
    BACKWARD = 1,
}

export interface FwStateKey {
    proto?: number;
    src_port?: number;
    dst_port?: number;
    src_addr?: IPAddressWire;
    dst_addr?: IPAddressWire;
}

export interface FwStateValue {
    external?: boolean;
    flags?: number;
    created_at?: number | string;
    updated_at?: number | string;
    packets_backward?: number | string;
    packets_forward?: number | string;
}

export interface FwStateEntry {
    key?: FwStateKey;
    value?: FwStateValue;
    idx?: number | string;
    expired?: boolean;
}

export interface ListEntriesRequest {
    config_name?: string;
    is_ipv6?: boolean;
    layer_index?: number;
    include_expired?: boolean;
    direction?: Direction;
    batch_size?: number;
    index?: number;
}

export interface ListEntriesResponse {
    entries?: FwStateEntry[];
    has_more?: boolean;
    index?: number | string;
    generation?: number | string;
}

export interface ShowConfigRequest {
    name?: string;
    ok_if_not_found?: boolean;
}

export interface UpdateConfigRequest {
    name?: string;
    sync_config?: SyncConfig;
    // References the standalone fwstate-map by name. Required: the server
    // resolves the named map's v4/v6 offsets and attaches them to the sync
    // config. This is the only way to supply the map pair.
    map_name?: string;
}

export interface DeleteConfigRequest {
    name?: string;
}

export interface GetStatsRequest {
    name?: string;
}

// FWStateMapService request/response types. The map service is a separate
// gRPC service (FWStateMapService) managing standalone named fwstate-map
// objects. GetMapStats reuses the same MapStats / GetStatsResponse shape as
// FWStateService.GetStats since both are wire-identical.

export interface CreateMapRequest {
    name?: string;
    index_size?: number;
    extra_bucket_count?: number;
    worker_count?: number;
}

export interface DeleteMapRequest {
    name?: string;
}

export interface ListMapsResponse {
    maps?: string[];
}

export interface GetMapStatsRequest {
    name?: string;
}

export interface InsertLayerRequest {
    name?: string;
    index_size?: number;
    extra_bucket_count?: number;
    worker_count?: number;
}

const fwStateService = createService('modules.fwstate.controlplane.fwstatepb.v1.FWStateService');
const fwStateStreamingService = createStreamingService('modules.fwstate.controlplane.fwstatepb.v1.FWStateService');
const fwStateMapService = createService('modules.fwstate.controlplane.fwstatepb.v1.FWStateMapService');

export const fwstate = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> =>
        fwStateService.call<ListConfigsResponse>('ListConfigs', options),

    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        fwStateService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),

    updateConfig: (request: UpdateConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('UpdateConfig', request, options),

    deleteConfig: (request: DeleteConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('DeleteConfig', request, options),

    getStats: (request: GetStatsRequest, options?: CallOptions): Promise<GetStatsResponse> =>
        fwStateService.callWithBody<GetStatsResponse>('GetStats', request, options),

    listEntriesPage: (
        request: ListEntriesRequest,
        callbacks: StreamCallbacks<ListEntriesResponse>,
        signal?: AbortSignal,
    ): void => {
        fwStateStreamingService.stream<ListEntriesResponse>('ListEntries', request, callbacks, signal);
    },

    createMap: (request: CreateMapRequest, options?: CallOptions): Promise<void> =>
        fwStateMapService.callWithBody<void>('CreateMap', request, options),

    deleteMap: (request: DeleteMapRequest, options?: CallOptions): Promise<void> =>
        fwStateMapService.callWithBody<void>('DeleteMap', request, options),

    listMaps: (options?: CallOptions): Promise<ListMapsResponse> =>
        fwStateMapService.call<ListMapsResponse>('ListMaps', options),

    getMapStats: (request: GetMapStatsRequest, options?: CallOptions): Promise<GetStatsResponse> =>
        fwStateMapService.callWithBody<GetStatsResponse>('GetMapStats', request, options),

    insertLayer: (request: InsertLayerRequest, options?: CallOptions): Promise<void> =>
        fwStateMapService.callWithBody<void>('InsertLayer', request, options),
};
