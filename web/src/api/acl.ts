import { createService } from './client';
import type { TargetModule } from './common';

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

export enum ActionKind {
    PASS = 0,
    DENY = 1,
    COUNT = 2,
    CHECK_STATE = 3,
    CREATE_STATE = 4,
}

export interface Rule {
    srcs?: IPNet[];
    dsts?: IPNet[];
    srcPortRanges?: PortRange[];
    dstPortRanges?: PortRange[];
    devices?: string[];
    vlanRanges?: VlanRange[];
    protoRanges?: ProtoRange[];
    keepState?: boolean;
    counter?: string;
    action?: ActionKind;
}

export interface MapConfig {
    indexSize?: number;
    extraBucketCount?: number;
}

export interface SyncConfig {
    srcAddr?: string | Uint8Array | number[];
    dstEther?: string | Uint8Array | number[];
    dstAddrMulticast?: string | Uint8Array | number[];
    portMulticast?: number;
    dstAddrUnicast?: string | Uint8Array | number[];
    portUnicast?: number;
    tcpSynAck?: number | string; // nanoseconds
    tcpSyn?: number | string;
    tcpFin?: number | string;
    tcp?: number | string;
    udp?: number | string;
    default?: number | string;
}

// Request/Response types

export interface AclUpdateConfigRequest {
    target?: TargetModule;
    rules?: Rule[];
}

export interface AclUpdateConfigResponse { }

export interface AclShowConfigRequest {
    target?: TargetModule;
}

export interface AclShowConfigResponse {
    target?: TargetModule;
    rules?: Rule[];
    fwstateMap?: MapConfig;
    fwstateSync?: SyncConfig;
}

export interface AclDeleteConfigRequest {
    target?: TargetModule;
}

export interface AclDeleteConfigResponse {
    deleted?: boolean;
}

export interface AclListConfigsRequest { }

export interface AclListConfigsResponse {
    configs?: string[];
}

export interface AclUpdateFWStateConfigRequest {
    target?: TargetModule;
    mapConfig?: MapConfig;
    syncConfig?: SyncConfig;
}

export interface AclUpdateFWStateConfigResponse { }

const aclService = createService('aclpb.ACLService');

export const acl = {
    listConfigs: (signal?: AbortSignal): Promise<AclListConfigsResponse> => {
        return aclService.call<AclListConfigsResponse>('ListConfigs', signal);
    },
    showConfig: (request: AclShowConfigRequest, signal?: AbortSignal): Promise<AclShowConfigResponse> => {
        return aclService.callWithBody<AclShowConfigResponse>('ShowConfig', request, signal);
    },
    updateConfig: (request: AclUpdateConfigRequest, signal?: AbortSignal): Promise<AclUpdateConfigResponse> => {
        return aclService.callWithBody<AclUpdateConfigResponse>('UpdateConfig', request, signal);
    },
    deleteConfig: (request: AclDeleteConfigRequest, signal?: AbortSignal): Promise<AclDeleteConfigResponse> => {
        return aclService.callWithBody<AclDeleteConfigResponse>('DeleteConfig', request, signal);
    },
    updateFWStateConfig: (request: AclUpdateFWStateConfigRequest, signal?: AbortSignal): Promise<AclUpdateFWStateConfigResponse> => {
        return aclService.callWithBody<AclUpdateFWStateConfigResponse>('UpdateFWStateConfig', request, signal);
    },
};
