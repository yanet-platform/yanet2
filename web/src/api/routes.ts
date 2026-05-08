import { createService, type CallOptions } from './client';
import type { MACAddress } from './neighbours';

// Route types

export enum RouteSourceID {
    UNKNOWN = 0,
    STATIC = 1,
    BIRD = 2,
}

export interface LargeCommunity {
    global_administrator?: number;
    local_data_part1?: number;
    local_data_part2?: number;
}

export interface Route {
    prefix?: string;
    next_hop?: string;
    peer?: string;
    route_distinguisher?: string | number; // uint64
    peer_as?: number;
    origin_as?: number;
    med?: number;
    pref?: number;
    as_path_len?: number;
    source?: number; // RouteSourceID enum
    large_communities?: LargeCommunity[];
    is_best?: boolean;
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowRoutesRequest {
    name?: string;
    ipv4_only?: boolean;
    ipv6_only?: boolean;
}

export interface ShowRoutesResponse {
    routes?: Route[];
}

export interface InsertRouteRequest {
    name?: string;
    prefix?: string;
    nexthop_addr?: string;
    do_flush?: boolean;
    source_id?: RouteSourceID;
}

export interface InsertRouteResponse {
}

export interface DeleteRouteRequest {
    name?: string;
    prefix?: string;
    nexthop_addr?: string;
    do_flush?: boolean;
    source_id?: RouteSourceID;
}

export interface DeleteRouteResponse {
}

export interface FlushRoutesRequest {
    name?: string;
}

export interface FlushRoutesResponse {
}

// FIB types

export interface ShowFIBRequest {
    name?: string;
    ipv4_only?: boolean;
    ipv6_only?: boolean;
}

export interface ShowFIBResponse {
    entries?: FIBEntry[];
}

export interface FIBEntry {
    prefix?: string;
    nexthops?: FIBNexthop[];
}

export interface FIBNexthop {
    dst_mac?: MACAddress;
    src_mac?: MACAddress;
    device?: string;
}

const routeService = createService('routepb.RouteService');
const operatorRouteService = createService('operators.route.operatorpb.v1.RouteService');

export const route = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> => {
        return operatorRouteService.call<ListConfigsResponse>('ListConfigs', options);
    },
    showRoutes: (request: ShowRoutesRequest, options?: CallOptions): Promise<ShowRoutesResponse> => {
        return operatorRouteService.callWithBody<ShowRoutesResponse>('ShowRoutes', request, options);
    },
    insertRoute: (request: InsertRouteRequest, options?: CallOptions): Promise<InsertRouteResponse> => {
        return operatorRouteService.callWithBody<InsertRouteResponse>('InsertRoute', request, options);
    },
    deleteRoute: (request: DeleteRouteRequest, options?: CallOptions): Promise<DeleteRouteResponse> => {
        return operatorRouteService.callWithBody<DeleteRouteResponse>('DeleteRoute', request, options);
    },
    flushRoutes: (request: FlushRoutesRequest, options?: CallOptions): Promise<FlushRoutesResponse> => {
        return operatorRouteService.callWithBody<FlushRoutesResponse>('FlushRoutes', request, options);
    },
    showFIB: (request: ShowFIBRequest, options?: CallOptions): Promise<ShowFIBResponse> => {
        return routeService.callWithBody<ShowFIBResponse>('ShowFIB', request, options);
    },
};
