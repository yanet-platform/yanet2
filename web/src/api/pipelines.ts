import { createService, type CallOptions } from './client';
import type { FunctionId } from './common';

// Pipeline types based on pipeline.proto

export interface PipelineId {
    name?: string;
}

export type { FunctionId };

export interface Pipeline {
    id?: PipelineId;
    functions?: FunctionId[];
}

export interface ListPipelinesRequest { }

export interface ListPipelinesResponse {
    ids?: PipelineId[];
}

export interface GetPipelineRequest {
    id?: PipelineId;
}

export interface GetPipelineResponse {
    pipeline?: Pipeline;
}

export interface UpdatePipelineRequest {
    pipeline?: Pipeline;
}

export interface UpdatePipelineResponse { }

export interface DeletePipelineRequest {
    id?: PipelineId;
}

export interface DeletePipelineResponse { }

const pipelineService = createService('ynpb.PipelineService');

export const pipelines = {
    list: (request: ListPipelinesRequest, options?: CallOptions): Promise<ListPipelinesResponse> => {
        return pipelineService.callWithBody<ListPipelinesResponse>('List', request, options);
    },
    get: (request: GetPipelineRequest, options?: CallOptions): Promise<GetPipelineResponse> => {
        return pipelineService.callWithBody<GetPipelineResponse>('Get', request, options);
    },
    update: (request: UpdatePipelineRequest, options?: CallOptions): Promise<UpdatePipelineResponse> => {
        return pipelineService.callWithBody<UpdatePipelineResponse>('Update', request, options);
    },
    delete: (request: DeletePipelineRequest, options?: CallOptions): Promise<DeletePipelineResponse> => {
        return pipelineService.callWithBody<DeletePipelineResponse>('Delete', request, options);
    },
};
