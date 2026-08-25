import { createService, type CallOptions } from './client';

export interface ShowConfigRequest {
    name?: string;
}

// The prefix lists are family-typed (commonpb.IPv4Prefix and
// commonpb.IPv6Prefix), serialized by the gateway as bare CIDR strings.
export interface ShowConfigResponse {
    prefixes4?: string[];
    prefixes6?: string[];
}

export interface DecapUpdateConfigRequest {
    name?: string;
    prefixes4?: string[];
    prefixes6?: string[];
}

export interface DecapUpdateConfigResponse { }

const decapService = createService('modules.decap.controlplane.decappb.v1.DecapService');

export const decap = {
    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        decapService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),
    updateConfig: (request: DecapUpdateConfigRequest, options?: CallOptions): Promise<DecapUpdateConfigResponse> =>
        decapService.callWithBody<DecapUpdateConfigResponse>('UpdateConfig', request, options),
};
