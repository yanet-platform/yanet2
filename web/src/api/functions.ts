import { createService } from './client';
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

export interface ListFunctionsRequest {
    instance?: number;
}

export interface ListFunctionsResponse {
    ids?: FunctionId[];
}

export interface GetFunctionRequest {
    instance?: number;
    id?: FunctionId;
}

export interface GetFunctionResponse {
    function?: Function;
}

export interface UpdateFunctionRequest {
    instance?: number;
    function?: Function;
}

export interface UpdateFunctionResponse { }

export interface DeleteFunctionRequest {
    instance?: number;
    id?: FunctionId;
}

export interface DeleteFunctionResponse { }

const functionService = createService('ynpb.FunctionService');

export const functions = {
    list: (request: ListFunctionsRequest, signal?: AbortSignal): Promise<ListFunctionsResponse> => {
        return functionService.callWithBody<ListFunctionsResponse>('List', request, signal);
    },
    get: (request: GetFunctionRequest, signal?: AbortSignal): Promise<GetFunctionResponse> => {
        return functionService.callWithBody<GetFunctionResponse>('Get', request, signal);
    },
    update: (request: UpdateFunctionRequest, signal?: AbortSignal): Promise<UpdateFunctionResponse> => {
        return functionService.callWithBody<UpdateFunctionResponse>('Update', request, signal);
    },
    delete: (request: DeleteFunctionRequest, signal?: AbortSignal): Promise<DeleteFunctionResponse> => {
        return functionService.callWithBody<DeleteFunctionResponse>('Delete', request, signal);
    },
};
