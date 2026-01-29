import { createService, type CallOptions } from './client';

// Route types

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
}

export interface InsertRouteResponse {
}

export interface DeleteRouteRequest {
    name?: string;
    prefix?: string;
    nexthop_addr?: string;
    do_flush?: boolean;
}

export interface DeleteRouteResponse {
}

export interface FlushRoutesRequest {
    name?: string;
}

export interface FlushRoutesResponse {
}

const routeService = createService('routepb.RouteService');

export const route = {
    listConfigs: (options?: CallOptions): Promise<ListConfigsResponse> => {
        return routeService.call<ListConfigsResponse>('ListConfigs', options);
    },
    showRoutes: (request: ShowRoutesRequest, options?: CallOptions): Promise<ShowRoutesResponse> => {
        return routeService.callWithBody<ShowRoutesResponse>('ShowRoutes', request, options);
    },
    insertRoute: (request: InsertRouteRequest, options?: CallOptions): Promise<InsertRouteResponse> => {
        return routeService.callWithBody<InsertRouteResponse>('InsertRoute', request, options);
    },
    deleteRoute: (request: DeleteRouteRequest, options?: CallOptions): Promise<DeleteRouteResponse> => {
        return routeService.callWithBody<DeleteRouteResponse>('DeleteRoute', request, options);
    },
    flushRoutes: (request: FlushRoutesRequest, options?: CallOptions): Promise<FlushRoutesResponse> => {
        return routeService.callWithBody<FlushRoutesResponse>('FlushRoutes', request, options);
    },
};
