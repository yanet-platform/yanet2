import { createService, type CallOptions } from './client';
import type { FunctionId } from './common';

// Function types based on function.proto

export interface ModuleId {
    type?: string;
    name?: string;
}

export interface Chain {
    name?: string;
    modules?: ModuleId[];
}

export interface FunctionChain {
    chain?: Chain;
    weight?: string | number; // uint64
}

export interface Function {
    id?: FunctionId;
    chains?: FunctionChain[];
}

export interface ListFunctionsRequest { }

export interface ListFunctionsResponse {
    ids?: FunctionId[];
}

export interface GetFunctionRequest {
    id?: FunctionId;
}

export interface GetFunctionResponse {
    function?: Function;
}

export interface UpdateFunctionRequest {
    function?: Function;
}

export interface UpdateFunctionResponse { }

export interface DeleteFunctionRequest {
    id?: FunctionId;
}

export interface DeleteFunctionResponse { }

const functionService = createService('ynpb.FunctionService');

export const functions = {
    list: (request: ListFunctionsRequest, options?: CallOptions): Promise<ListFunctionsResponse> => {
        return functionService.callWithBody<ListFunctionsResponse>('List', request, options);
    },
    get: (request: GetFunctionRequest, options?: CallOptions): Promise<GetFunctionResponse> => {
        return functionService.callWithBody<GetFunctionResponse>('Get', request, options);
    },
    update: (request: UpdateFunctionRequest, options?: CallOptions): Promise<UpdateFunctionResponse> => {
        return functionService.callWithBody<UpdateFunctionResponse>('Update', request, options);
    },
    delete: (request: DeleteFunctionRequest, options?: CallOptions): Promise<DeleteFunctionResponse> => {
        return functionService.callWithBody<DeleteFunctionResponse>('Delete', request, options);
    },
};
