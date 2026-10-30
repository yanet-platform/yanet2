import { createService, type CallOptions } from './client';

export interface SyncEndpoint {
    // commonpb.IPAddress, serialized by the gateway as a bare IP string.
    src_addr?: string;
    dst_addr?: string;
    port?: number;
}

export interface SyncListenEndpoint {
    // commonpb.IPAddress, serialized by the gateway as a bare IP string.
    dst_addr?: string;
    port?: number;
}

export interface SyncConfig {
    endpoints?: SyncEndpoint[];
    // Removes every stored endpoint when true.
    no_endpoints?: boolean;
    // Receive destinations; independent of the emission endpoints.
    listeners?: SyncListenEndpoint[];
    // Removes every stored listener when true.
    no_listeners?: boolean;
    tcp_syn_ack?: number;
    tcp_syn?: number;
    tcp_fin?: number;
    tcp?: number;
    udp?: number;
    default?: number;
    // Largest emitted sync packet in bytes, IPv6+UDP headers included; 0
    // selects the service default of 1500.
    sync_mtu?: number;
}

export interface ShowConfigResponse {
    name?: string;
    map_name_v4?: string;
    map_name_v6?: string;
    sync_config?: SyncConfig;
}

import type { ListConfigsResponse } from './shared';
export type { ListConfigsResponse };

export interface ShowConfigRequest {
    name?: string;
    ok_if_not_found?: boolean;
}

export interface UpdateConfigRequest {
    name?: string;
    map_name_v4?: string;
    map_name_v6?: string;
    sync_config?: SyncConfig;
}

export interface DeleteConfigRequest {
    name?: string;
}

const fwStateService = createService('modules.fwstate.controlplane.fwstatepb.v1.FWStateService');

export const fwstate = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> =>
        fwStateService.call<ListConfigsResponse>('ListConfigs', options),

    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        fwStateService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),

    updateConfig: (request: UpdateConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('UpdateConfig', request, options),

    deleteConfig: (request: DeleteConfigRequest, options?: CallOptions): Promise<void> =>
        fwStateService.callWithBody<void>('DeleteConfig', request, options),
};
