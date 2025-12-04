import { createService } from './client';

// Pipeline types based on pipeline.proto

export interface PipelineId {
    name?: string;
}

export interface FunctionId {
    name?: string;
}

export interface Pipeline {
    id?: PipelineId;
    functions?: FunctionId[];
}

export interface ListPipelinesRequest {
    instance?: number;
}

export interface ListPipelinesResponse {
    ids?: PipelineId[];
}

export interface GetPipelineRequest {
    instance?: number;
    id?: PipelineId;
}

export interface GetPipelineResponse {
    pipeline?: Pipeline;
}

export interface UpdatePipelineRequest {
    instance?: number;
    pipeline?: Pipeline;
}

export interface UpdatePipelineResponse { }

export interface DeletePipelineRequest {
    instance?: number;
    id?: PipelineId;
}

export interface DeletePipelineResponse { }

const pipelineService = createService('ynpb.PipelineService');

export const pipelines = {
    list: (request: ListPipelinesRequest, signal?: AbortSignal): Promise<ListPipelinesResponse> => {
        return pipelineService.callWithBody<ListPipelinesResponse>('List', request, signal);
    },
    get: (request: GetPipelineRequest, signal?: AbortSignal): Promise<GetPipelineResponse> => {
        return pipelineService.callWithBody<GetPipelineResponse>('Get', request, signal);
    },
    update: (request: UpdatePipelineRequest, signal?: AbortSignal): Promise<UpdatePipelineResponse> => {
        return pipelineService.callWithBody<UpdatePipelineResponse>('Update', request, signal);
    },
    delete: (request: DeletePipelineRequest, signal?: AbortSignal): Promise<DeletePipelineResponse> => {
        return pipelineService.callWithBody<DeletePipelineResponse>('Delete', request, signal);
    },
};
