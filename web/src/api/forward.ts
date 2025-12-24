import { createService, type CallOptions } from './client';

// Forward types based on forwardpb/forward.proto

export interface Device {
    name?: string;
}

export interface VlanRange {
    from?: number;
    to?: number;
}

export interface IPNet {
    addr?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
    mask?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
}

export enum ForwardMode {
    NONE = 0,
    IN = 1,
    OUT = 2,
}

export const FORWARD_MODE_LABELS: Record<ForwardMode, string> = {
    [ForwardMode.NONE]: 'NONE',
    [ForwardMode.IN]: 'IN',
    [ForwardMode.OUT]: 'OUT',
};

export interface Action {
    target?: string;
    mode?: ForwardMode;
    counter?: string;
}

export interface Rule {
    action?: Action;
    devices?: Device[];
    vlanRanges?: VlanRange[];
    srcs?: IPNet[];
    dsts?: IPNet[];
}

// Request/Response types

export interface ListConfigsRequest { }

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    name?: string;
    rules?: Rule[];
}

export interface UpdateConfigRequest {
    name?: string;
    rules?: Rule[];
}

export interface UpdateConfigResponse {
    error?: string;
}

export interface DeleteConfigRequest {
    name?: string;
}

export interface DeleteConfigResponse {
    deleted?: boolean;
}

const forwardService = createService('forwardpb.ForwardService');

export const forward = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> => {
        return forwardService.call<ListConfigsResponse>('ListConfigs', options);
    },
    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> => {
        return forwardService.callWithBody<ShowConfigResponse>('ShowConfig', request, options);
    },
    updateConfig: (request: UpdateConfigRequest, options?: CallOptions): Promise<UpdateConfigResponse> => {
        return forwardService.callWithBody<UpdateConfigResponse>('UpdateConfig', request, options);
    },
    deleteConfig: (request: DeleteConfigRequest, options?: CallOptions): Promise<DeleteConfigResponse> => {
        return forwardService.callWithBody<DeleteConfigResponse>('DeleteConfig', request, options);
    },
};
