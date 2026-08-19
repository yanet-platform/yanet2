import { createService, type CallOptions } from './client';
import type { ContiguousIPNetwork } from '../utils/netip';

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    prefixes?: ContiguousIPNetwork[];
}

export interface DecapUpdateConfigRequest {
    name?: string;
    prefixes?: ContiguousIPNetwork[];
}

export interface DecapUpdateConfigResponse { }

const decapService = createService('modules.decap.controlplane.decappb.v1.DecapService');

export const decap = {
    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> =>
        decapService.callWithBody<ShowConfigResponse>('ShowConfig', request, options),
    updateConfig: (request: DecapUpdateConfigRequest, options?: CallOptions): Promise<DecapUpdateConfigResponse> =>
        decapService.callWithBody<DecapUpdateConfigResponse>('UpdateConfig', request, options),
};
