import { createService, type CallOptions } from './client';

// Types matching aclpb/acl.proto and filterpb/filter.proto exactly.
// No Action.counter, no keep_state, no MapConfig, no SyncConfig, no DUMP kind.

export interface IPNet {
    addr?: string | Uint8Array | number[];
    mask?: string | Uint8Array | number[];
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

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    name?: string;
    rules?: Rule[];
    fwstate_name?: string;
}

export interface UpdateConfigRequest {
    name?: string;
    rules?: Rule[];
}

export interface UpdateConfigResponse {}

export interface DeleteConfigRequest {
    name?: string;
}

export interface DeleteConfigResponse {
    deleted?: boolean;
}

const aclService = createService('aclpb.ACLService');

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
