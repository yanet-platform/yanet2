import { createService, type CallOptions } from './client';
import type { IPPrefix } from '../utils/netip';

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    prefixes?: IPPrefix[];
}

export interface DecapUpdateConfigRequest {
    name?: string;
    prefixes?: IPPrefix[];
}

export interface DecapUpdateConfigResponse { }

const decapService = createService('modules.decap.controlplane.decappb.v1.DecapService');

export const decap = {
    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        decapService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),
    updateConfig: (request: DecapUpdateConfigRequest, options?: CallOptions): Promise<DecapUpdateConfigResponse> =>
        decapService.callWithBody<DecapUpdateConfigResponse>('UpdateConfig', request, options),
};
