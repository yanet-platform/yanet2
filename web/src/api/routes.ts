import { createService, type CallOptions } from './client';

// Route types

export interface LargeCommunity {
    globalAdministrator?: number; // global_administrator
    localDataPart1?: number; // local_data_part1
    localDataPart2?: number; // local_data_part2
}

export interface Route {
    prefix?: string;
    nextHop?: string; // next_hop
    peer?: string;
    routeDistinguisher?: string | number; // route_distinguisher (uint64)
    peerAs?: number; // peer_as
    originAs?: number; // origin_as
    med?: number;
    pref?: number;
    asPathLen?: number; // as_path_len
    source?: number; // RouteSourceID enum
    largeCommunities?: LargeCommunity[]; // large_communities
    isBest?: boolean; // is_best
}

export interface ListConfigsResponse {
    configs?: string[];
}

export interface ShowRoutesRequest {
    name?: string;
    ipv4Only?: boolean; // ipv4_only
    ipv6Only?: boolean; // ipv6_only
}

export interface ShowRoutesResponse {
    routes?: Route[];
}

export interface InsertRouteRequest {
    name?: string;
    prefix?: string;
    nexthopAddr?: string; // nexthop_addr
    doFlush?: boolean; // do_flush
}

export interface InsertRouteResponse {
}

export interface DeleteRouteRequest {
    name?: string;
    prefix?: string;
    nexthopAddr?: string; // nexthop_addr
    doFlush?: boolean; // do_flush
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
