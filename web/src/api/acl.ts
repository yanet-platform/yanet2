import { createService, type CallOptions } from './client';

// ACL types based on aclpb/acl.proto

export interface IPNet {
    addr?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
    mask?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
}

export interface PortRange {
    from?: number;
    to?: number;
}

export interface ProtoRange {
    from?: number;
    to?: number;
}

export interface VlanRange {
    from?: number;
    to?: number;
}

export interface Device {
    name?: string;
}

export enum ActionKind {
    PASS = 0,
    DENY = 1,
    COUNT = 2,
    CHECK_STATE = 3,
    CREATE_STATE = 4,
}

export interface Action {
    kind?: ActionKind;
    counter?: string;
    keep_state?: boolean;
}

export interface Rule {
    action?: Action;
    srcs?: IPNet[];
    dsts?: IPNet[];
    src_port_ranges?: PortRange[];
    dst_port_ranges?: PortRange[];
    devices?: Device[];
    vlan_ranges?: VlanRange[];
    proto_ranges?: ProtoRange[];
}

export interface MapConfig {
    index_size?: number;
    extra_bucket_count?: number;
}

export interface SyncConfig {
    src_addr?: string | Uint8Array | number[];
    dst_ether?: string | Uint8Array | number[];
    dst_addr_multicast?: string | Uint8Array | number[];
    port_multicast?: number;
    dst_addr_unicast?: string | Uint8Array | number[];
    port_unicast?: number;
    tcp_syn_ack?: number | string; // nanoseconds
    tcp_syn?: number | string;
    tcp_fin?: number | string;
    tcp?: number | string;
    udp?: number | string;
    default?: number | string;
}

// Request/Response types

export interface AclUpdateConfigRequest {
    name?: string;
    rules?: Rule[];
}

export interface AclUpdateConfigResponse { }

export interface AclShowConfigRequest {
    name?: string;
}

export interface AclShowConfigResponse {
    name?: string;
    rules?: Rule[];
    fwstate_map?: MapConfig;
    fwstate_sync?: SyncConfig;
}

export interface AclDeleteConfigRequest {
    name?: string;
}

export interface AclDeleteConfigResponse {
    deleted?: boolean;
}

export interface AclListConfigsRequest { }

export interface AclListConfigsResponse {
    configs?: string[];
}

export interface AclUpdateFWStateConfigRequest {
    name?: string;
    map_config?: MapConfig;
    sync_config?: SyncConfig;
}

export interface AclUpdateFWStateConfigResponse { }

const aclService = createService('aclpb.ACLService');

export const acl = {
    listConfigs: (options?: CallOptions): Promise<AclListConfigsResponse> => {
        return aclService.call<AclListConfigsResponse>('ListConfigs', options);
    },
    showConfig: (request: AclShowConfigRequest, options?: CallOptions): Promise<AclShowConfigResponse> => {
        return aclService.callWithBody<AclShowConfigResponse>('ShowConfig', request, options);
    },
    updateConfig: (request: AclUpdateConfigRequest, options?: CallOptions): Promise<AclUpdateConfigResponse> => {
        return aclService.callWithBody<AclUpdateConfigResponse>('UpdateConfig', request, {
            compress: true,
            ...options,
        });
    },
    deleteConfig: (request: AclDeleteConfigRequest, options?: CallOptions): Promise<AclDeleteConfigResponse> => {
        return aclService.callWithBody<AclDeleteConfigResponse>('DeleteConfig', request, options);
    },
    updateFWStateConfig: (request: AclUpdateFWStateConfigRequest, options?: CallOptions): Promise<AclUpdateFWStateConfigResponse> => {
        return aclService.callWithBody<AclUpdateFWStateConfigResponse>('UpdateFWStateConfig', request, options);
    },
};
