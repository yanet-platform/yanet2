import { createService, type CallOptions } from './client';

// Decap types based on decap.proto

export interface ShowConfigRequest {
    name?: string;
}

export interface ShowConfigResponse {
    prefixes?: string[];
}

export interface AddPrefixesRequest {
    name?: string;
    prefixes?: string[];
}

export interface AddPrefixesResponse { }

export interface RemovePrefixesRequest {
    name?: string;
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
