import { createService, type CallOptions } from './client';

// Capacity and publish-batch limits mirror the request checks in
// objects/ring/controlplane/ringpb/v1/ring.go.
//
// A capacity must be a power of two that fits 32 bits; that is the request's
// bound, not what a ring may actually take. The dataplane allocator caps a
// ring far lower, at a build-dependent size only the service can check. The
// publish batch is 0 (the service default) through the batch maximum.
export const RING_MAX_CAPACITY = 2 ** 32 - 1;
export const RING_DEFAULT_PUBLISH_BATCH = 8;
export const RING_MAX_PUBLISH_BATCH = 1024;

// RingInfo describes one registered ring. capacity is protojson uint64, so
// it arrives as either a number or a numeric string.
export interface RingInfo {
    name?: string;
    capacity?: number | string;
    publish_batch?: number;
}

export interface CreateRingRequest {
    name: string;
    capacity: number | string;
    publish_batch?: number;
}

export interface CreateRingResponse {}

export interface ListRingsResponse {
    rings?: RingInfo[];
}

export interface ShowRingRequest {
    name: string;
}

export interface ShowRingResponse {
    ring?: RingInfo;
}

export interface DeleteRingRequest {
    name: string;
}

export interface DeleteRingResponse {}

const ringService = createService('objects.ring.controlplane.ringpb.v1.RingService');

export const ring = {
    // capacity must be a power of two; publish_batch of 0 or omitted selects
    // the service default of 8.
    createRing: (request: CreateRingRequest, options?: CallOptions): Promise<CreateRingResponse> =>
        ringService.callWithBody<CreateRingResponse>(
            'CreateRing',
            { name: request.name, capacity: request.capacity, publish_batch: request.publish_batch },
            options,
        ),

    listRings: (options?: CallOptions): Promise<ListRingsResponse> =>
        ringService.callWithBody<ListRingsResponse>('ListRings', {}, options),

    showRing: (request: ShowRingRequest, options?: CallOptions): Promise<ShowRingResponse> =>
        ringService.callWithBody<ShowRingResponse>('ShowRing', { name: request.name }, options),

    // Fails with FailedPrecondition while a pdump config still links the ring.
    deleteRing: (request: DeleteRingRequest, options?: CallOptions): Promise<DeleteRingResponse> =>
        ringService.callWithBody<DeleteRingResponse>('DeleteRing', { name: request.name }, options),
};
