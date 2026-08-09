import { createService, type CallOptions } from './client';

// Types matching aclpb/acl.proto and filterpb/filter.proto exactly.
// No Action.counter, no keep_state, no DUMP kind.

import type { IPNet, VlanRange, Device, ListConfigsResponse } from './shared';
import type { IPAddressWire } from '../utils/netip';
export type { IPNet, VlanRange, Device, ListConfigsResponse };

// SyncConfig mirrors modules.fwstate.controlplane.fwstatepb.v1.SyncConfig.
//
// The proto deliberately duplicates the message (the C-level coupling through
// lib/fwstate/config.h already exists), so the wire-format duplication is
// mirrored here rather than imported cross-module. Field numbers and types
// stay identical to the fwstate proto.
export interface SyncConfig {
    src_addr?: IPAddressWire;
    dst_ether?: { addr: string };
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
}

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    name?: string;
    rules?: Rule[];
    // Name of the standalone fwstate-map this ACL config references.
    map_name?: string;
    // Synchronization parameters for CREATE_STATE sync packets.
    sync_config?: SyncConfig;
}

export interface UpdateConfigRequest {
    name?: string;
    rules?: Rule[];
    // References the standalone fwstate-map by name. Required when any rule
    // uses ACTION_KIND_CREATE_STATE; allowed for CHECK_STATE-only rulesets
    // that borrow the map to read state.
    map_name?: string;
    // Required iff any rule uses ACTION_KIND_CREATE_STATE; otherwise ignored.
    sync_config?: SyncConfig;
}

export interface UpdateConfigResponse {}

export interface DeleteConfigRequest {
    name?: string;
}

export interface DeleteConfigResponse {
    deleted?: boolean;
}

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
