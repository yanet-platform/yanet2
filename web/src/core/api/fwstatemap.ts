import { createService, type CallOptions } from './client';
import type { IPAddressWire } from '../utils/netip';

export interface MapStats {
    index_size?: number;
    extra_bucket_count?: number;
    max_chain_length?: number;
    layer_count?: number;
    total_elements?: number;
    max_deadline?: number;
    memory_used?: number;
    note?: string;
}

export enum Direction {
    FORWARD = 0,
    BACKWARD = 1,
}

export enum MapKind {
    V4 = 0,
    V6 = 1,
}

export interface CreateMapRequest {
    name: string;
    kind: MapKind;
}

export interface CreateMapResponse {}

export interface DeleteMapRequest {
    name: string;
}

export interface DeleteMapResponse {}

// maps lists every registered map name; kinds additionally names each
// map's family (MapKind value keyed by name), so callers can scope a
// name to a family without a per-map lookup.
export interface ListMapsResponse {
    maps?: string[];
    kinds?: Record<string, MapKind>;
}

export interface FwStateKey {
    proto?: number;
    src_port?: number;
    dst_port?: number;
    src_addr?: IPAddressWire;
    dst_addr?: IPAddressWire;
}

export interface FwStateValue {
    external?: boolean;
    flags?: number;
    created_at?: number | string;
    updated_at?: number | string;
    packets_backward?: number | string;
    packets_forward?: number | string;
}

export interface FwStateEntry {
    key?: FwStateKey;
    value?: FwStateValue;
    idx?: number | string;
    expired?: boolean;
}

export interface ListEntriesRequest {
    map_name?: string;
    layer_index?: number;
    include_expired?: boolean;
    direction?: Direction;
    batch_size?: number;
    index?: number;
}

export interface ListEntriesResponse {
    entries?: FwStateEntry[];
    has_more?: boolean;
    index?: number | string;
    generation?: number | string;
}

export interface GetMapStatsRequest {
    name?: string;
}

export interface GetMapStatsResponse {
    stats?: MapStats;
}

const fwStateMapService = createService('objects.fwstate.controlplane.fwstatemappb.v1.FWStateMapService');

export const fwstatemap = {
    getMapStats: (request: GetMapStatsRequest, options?: CallOptions): Promise<GetMapStatsResponse> =>
        fwStateMapService.callWithBody<GetMapStatsResponse>('GetMapStats', request, options),

    listMaps: (options?: CallOptions): Promise<ListMapsResponse> =>
        fwStateMapService.callWithBody<ListMapsResponse>('ListMaps', {}, options),

    // Only the name and the family are sent: leaving the sizing fields at
    // zero makes the service create the map with its default dimensions.
    createMap: (request: CreateMapRequest, options?: CallOptions): Promise<CreateMapResponse> =>
        fwStateMapService.callWithBody<CreateMapResponse>('CreateMap', { name: request.name, kind: request.kind }, options),

    deleteMap: (request: DeleteMapRequest, options?: CallOptions): Promise<DeleteMapResponse> =>
        fwStateMapService.callWithBody<DeleteMapResponse>('DeleteMap', { name: request.name }, options),

    // One cursor batch: the response's index feeds the next request until
    // has_more is false.
    listEntriesPage: (
        request: ListEntriesRequest,
        options?: CallOptions,
    ): Promise<ListEntriesResponse> =>
        fwStateMapService.callWithBody<ListEntriesResponse>('ListEntries', request, options),
};
