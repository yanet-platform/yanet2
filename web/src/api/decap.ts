import { createService, type CallOptions } from './client';
import type { TargetModule } from './common';

// Decap types based on decap.proto

export interface ShowConfigRequest {
    target?: TargetModule;
}

export interface ShowConfigResponse {
    prefixes?: string[];
}

export interface AddPrefixesRequest {
    target?: TargetModule;
    prefixes?: string[];
}

export interface AddPrefixesResponse { }

export interface RemovePrefixesRequest {
    target?: TargetModule;
    prefixes?: string[];
}

export interface RemovePrefixesResponse { }

const decapService = createService('decappb.DecapService');

export const decap = {
    showConfig: (request: ShowConfigRequest, options?: CallOptions): Promise<ShowConfigResponse> => {
        return decapService.callWithBody<ShowConfigResponse>('ShowConfig', request, options);
    },
    addPrefixes: (request: AddPrefixesRequest, options?: CallOptions): Promise<AddPrefixesResponse> => {
        return decapService.callWithBody<AddPrefixesResponse>('AddPrefixes', request, options);
    },
    removePrefixes: (request: RemovePrefixesRequest, options?: CallOptions): Promise<RemovePrefixesResponse> => {
        return decapService.callWithBody<RemovePrefixesResponse>('RemovePrefixes', request, options);
    },
};
