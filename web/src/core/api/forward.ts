import { createService, type CallOptions } from './client';

// Forward types based on forwardpb/forward.proto

import type { Device, VlanRange, ListConfigsResponse } from './shared';
export type { Device, VlanRange, ListConfigsResponse };

// The gateway serializes a declared mode by its proto name. A gateway that
// predates that form, or a value this build does not know, sends a number.
export enum ForwardMode {
    NONE = 'NONE',
    IN = 'IN',
    OUT = 'OUT',
}

export const FORWARD_MODE_LABELS: Record<ForwardMode, string> = {
    [ForwardMode.NONE]: 'NONE',
    [ForwardMode.IN]: 'IN',
    [ForwardMode.OUT]: 'OUT',
};

const FORWARD_MODE_BY_NUMBER: Record<number, ForwardMode> = {
    0: ForwardMode.NONE,
    1: ForwardMode.IN,
    2: ForwardMode.OUT,
};

/**
 * Maps a declared mode name or number to the mode, undefined for anything
 * this build does not declare.
 */
export const declaredForwardMode = (value: ForwardMode | number | string | undefined): ForwardMode | undefined => {
    if (typeof value === 'number') {
        return FORWARD_MODE_BY_NUMBER[value];
    }
    if (typeof value !== 'string') {
        return undefined;
    }
    // An own-value check, so an inherited object key such as "toString"
    // does not pass as a declared mode.
    return (Object.values(ForwardMode) as string[]).includes(value) ? (value as ForwardMode) : undefined;
};

/**
 * Reads a mode off the wire in either spelling.
 *
 * A name or a declared number maps to the mode. Anything else, including a
 * value this build does not know, reads as NONE, the same fallback the page
 * used before names existed.
 */
export const parseForwardMode = (value: ForwardMode | number | undefined): ForwardMode => {
    return declaredForwardMode(value) ?? ForwardMode.NONE;
};

export interface Action {
    target?: string;
    mode?: ForwardMode | number;
    counter?: string;
}

export interface Rule {
    action?: Action;
    devices?: Device[];
    vlan_ranges?: VlanRange[];
    // Family-typed network lists (commonpb.IPv4Network and
    // commonpb.IPv6Network), serialized by the gateway as bare network
    // strings.
    sources4?: string[];
    sources6?: string[];
    destinations4?: string[];
    destinations6?: string[];
}

// Request/Response types

export interface ListConfigsRequest { }

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

export interface UpdateConfigResponse {}

export interface DeleteConfigRequest {
    name?: string;
}

export interface DeleteConfigResponse {}

const forwardService = createService('modules.forward.controlplane.forwardpb.v1.ForwardService');

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
