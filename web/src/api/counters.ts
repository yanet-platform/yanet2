import { createService, type CallOptions } from './client';

// Counter types
export interface CounterInstanceInfo {
    values?: (string | number)[]; // uint64[] - serialized as string in JSON
}

export interface CounterInfo {
    name?: string;
    instances?: CounterInstanceInfo[];
}

export interface CountersResponse {
    counters?: CounterInfo[];
}

export interface DeviceCountersRequest {
    device: string;
}

export interface PipelineCountersRequest {
    device: string;
    pipeline: string;
}

export interface FunctionCountersRequest {
    device: string;
    pipeline: string;
    function: string;
}

export interface ChainCountersRequest {
    device: string;
    pipeline: string;
    function: string;
    chain: string;
}

export interface ModuleCountersRequest {
    device: string;
    pipeline: string;
    function: string;
    chain: string;
    module_type: string;
    module_name: string;
}

const countersService = createService('ynpb.CountersService');

export const counters = {
    device: (request: DeviceCountersRequest, options?: CallOptions): Promise<CountersResponse> => {
        return countersService.callWithBody<CountersResponse>('Device', request, options);
    },
    pipeline: (request: PipelineCountersRequest, options?: CallOptions): Promise<CountersResponse> => {
        return countersService.callWithBody<CountersResponse>('Pipeline', request, options);
    },
    function: (request: FunctionCountersRequest, options?: CallOptions): Promise<CountersResponse> => {
        return countersService.callWithBody<CountersResponse>('Function', request, options);
    },
    chain: (request: ChainCountersRequest, options?: CallOptions): Promise<CountersResponse> => {
        return countersService.callWithBody<CountersResponse>('Chain', request, options);
    },
    module: (request: ModuleCountersRequest, options?: CallOptions): Promise<CountersResponse> => {
        return countersService.callWithBody<CountersResponse>('Module', request, options);
    },
};
