import { createService } from './client';
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
    showConfig: (request: ShowConfigRequest, signal?: AbortSignal): Promise<ShowConfigResponse> => {
        return decapService.callWithBody<ShowConfigResponse>('ShowConfig', request, signal);
    },
    addPrefixes: (request: AddPrefixesRequest, signal?: AbortSignal): Promise<AddPrefixesResponse> => {
        return decapService.callWithBody<AddPrefixesResponse>('AddPrefixes', request, signal);
    },
    removePrefixes: (request: RemovePrefixesRequest, signal?: AbortSignal): Promise<RemovePrefixesResponse> => {
        return decapService.callWithBody<RemovePrefixesResponse>('RemovePrefixes', request, signal);
    },
};
