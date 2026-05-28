import { createService, type CallOptions } from './client';

// Counter types
export interface CounterInstanceInfo {
    values?: (string | number)[]; // uint64[] - serialized as string in JSON
}

export interface CounterInfo {
    name?: string;
    instances?: CounterInstanceInfo[];
}

export interface CounterTag {
    key?: string;
    value?: string;
}

export interface CountersByTagsRequest {
    tags?: CounterTag[];
    query?: string[];
}

export interface CounterGroup {
    tags?: CounterTag[];
    counters?: CounterInfo[];
}

export interface CountersByTagsResponse {
    groups?: CounterGroup[];
}

const countersService = createService('ynpb.CountersService');

export const counters = {
    byTags: (request: CountersByTagsRequest, options?: CallOptions): Promise<CountersByTagsResponse> => {
        return countersService.callWithBody<CountersByTagsResponse>('ByTags', request, options);
    },
};
