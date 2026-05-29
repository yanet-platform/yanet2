import { createService, createStreamingService, type CallOptions, type StreamCallbacks } from './client';
import type { IPAddressWire } from '../utils/netip';

export interface MapConfig {
    index_size?: number;
    extra_bucket_count?: number;
}

export interface SyncConfig {
    src_addr?: IPAddressWire;
    dst_ether?: string | Uint8Array | number[];
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
    linked_acls?: string[];
    map_config?: MapConfig;
    sync_config?: SyncConfig;
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface LinkFWStateRequest {
    fwstate_name?: string;
    acl_config_names?: string[];
}

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
    map_config?: MapConfig;
    sync_config?: SyncConfig;
}

export interface DeleteConfigRequest {
    name?: string;
}

export interface GetStatsRequest {
    name?: string;
}

const fwStateService = createService('fwstatepb.FWStateService');
const fwStateStreamingService = createStreamingService('fwstatepb.FWStateService');

export const fwstate = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> =>
        fwStateService.call<ListConfigsResponse>('ListConfigs', options),

    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        fwStateService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),

    updateConfig: (request: UpdateConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('UpdateConfig', request, options),

    deleteConfig: (request: DeleteConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('DeleteConfig', request, options),

    linkFWState: (request: LinkFWStateRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('LinkFWState', request, options),

    getStats: (request: GetStatsRequest, options?: CallOptions): Promise<GetStatsResponse> =>
        fwStateService.callWithBody<GetStatsResponse>('GetStats', request, options),

    listEntriesPage: (
        request: ListEntriesRequest,
        callbacks: StreamCallbacks<ListEntriesResponse>,
        signal?: AbortSignal,
    ): void => {
        fwStateStreamingService.stream<ListEntriesResponse>('ListEntries', request, callbacks, signal);
    },
};
