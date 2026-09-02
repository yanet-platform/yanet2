import { createService, type CallOptions } from './client';

// Types matching aclpb/acl.proto and filterpb/filter.proto exactly.
// No Action.counter, no keep_state, no MapConfig, no DUMP kind.

import type { MACAddress } from './neighbours';

import type { IPNet, VlanRange, Device, ListConfigsResponse } from './shared';
export type { IPNet, VlanRange, Device, ListConfigsResponse };

export interface PortRange {
    from?: number;
    to?: number;
}

export interface ProtoRange {
    from?: number;
    to?: number;
}

export enum ActionKind {
    ACTION_KIND_PASS = 0,
    ACTION_KIND_DENY = 1,
    ACTION_KIND_COUNT = 2,
    ACTION_KIND_CHECK_STATE = 4,
    ACTION_KIND_CREATE_STATE = 5,
    ACTION_KIND_LOG = 6,
}

/** Human-readable label for each ActionKind. */
export const ACTION_KIND_LABELS: Record<ActionKind, string> = {
    [ActionKind.ACTION_KIND_PASS]: 'pass',
    [ActionKind.ACTION_KIND_DENY]: 'deny',
    [ActionKind.ACTION_KIND_COUNT]: 'count',
    [ActionKind.ACTION_KIND_CHECK_STATE]: '?state',
    [ActionKind.ACTION_KIND_CREATE_STATE]: '+state',
    [ActionKind.ACTION_KIND_LOG]: 'log',
};

export interface Action {
    kind?: ActionKind;
}

export interface Rule {
    actions?: Action[];
    counter?: string;
    devices?: Device[];
    vlan_ranges?: VlanRange[];
    srcs?: IPNet[];
    dsts?: IPNet[];
    proto_ranges?: ProtoRange[];
    src_port_ranges?: PortRange[];
    dst_port_ranges?: PortRange[];
    // Family-typed network lists (commonpb.IPv4Network and
    // commonpb.IPv6Network), serialized by the gateway as bare network
    // strings. They merge with the legacy srcs/dsts lists.
    sources4?: string[];
    sources6?: string[];
    destinations4?: string[];
    destinations6?: string[];
}

export interface ShowConfigRequest {
    name?: string;
}

export interface SyncConfig {
    dst_ether?: MACAddress;
    // commonpb.IPAddress, serialized by the gateway as a bare IP string.
    dst_addr_multicast?: string;
    port_multicast?: number;
    dst_addr_unicast?: string;
    port_unicast?: number;
}

export interface ShowConfigResponse {
    name?: string;
    rules?: Rule[];
    fwtable_name_v4?: string;
    fwtable_name_v6?: string;
    sync_config?: SyncConfig;
}

export interface UpdateConfigRequest {
    name?: string;
    rules?: Rule[];
    fwtable_name_v4?: string;
    fwtable_name_v6?: string;
    sync_config?: SyncConfig;
}

export interface UpdateConfigResponse {}

export interface DeleteConfigRequest {
    name?: string;
}

export interface DeleteConfigResponse {}

const aclService = createService('modules.acl.controlplane.aclpb.v1.ACLService');

export const acl = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> =>
        aclService.call<ListConfigsResponse>('ListConfigs', options),

    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        aclService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),

    updateConfig: (request: UpdateConfigRequest, options?: CallOptions): Promise<UpdateConfigResponse> =>
        aclService.callWithBody<UpdateConfigResponse>('UpdateConfig', request, {
            compress: true,
            ...options,
        }),

    deleteConfig: (request: DeleteConfigRequest, options?: CallOptions): Promise<DeleteConfigResponse> =>
        aclService.callWithBody<DeleteConfigResponse>('DeleteConfig', request, options),
};
