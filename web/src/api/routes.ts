import { createService } from './client';

// Route types
export interface TargetModule {
    configName?: string; // config_name
    dataplaneInstance?: number; // dataplane_instance
}

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

export interface InstanceConfigs {
    instance?: number;
    configs?: string[];
}

export interface ListConfigsResponse {
    instanceConfigs?: InstanceConfigs[]; // instance_configs
}

export interface ShowRoutesRequest {
    target?: TargetModule;
    ipv4Only?: boolean; // ipv4_only
    ipv6Only?: boolean; // ipv6_only
}

export interface ShowRoutesResponse {
    routes?: Route[];
}

export interface InsertRouteRequest {
    target?: TargetModule;
    prefix?: string;
    nexthopAddr?: string; // nexthop_addr
    doFlush?: boolean; // do_flush
}

export interface InsertRouteResponse {
}

export interface DeleteRouteRequest {
    target?: TargetModule;
    prefix?: string;
    nexthopAddr?: string; // nexthop_addr
    doFlush?: boolean; // do_flush
}

export interface DeleteRouteResponse {
}

export interface FlushRoutesRequest {
    target?: TargetModule;
}

export interface FlushRoutesResponse {
}

const routeService = createService('routepb.RouteService');

export const route = {
    listConfigs: (signal?: AbortSignal): Promise<ListConfigsResponse> => {
        return routeService.call<ListConfigsResponse>('ListConfigs', signal);
    },
    showRoutes: (request: ShowRoutesRequest, signal?: AbortSignal): Promise<ShowRoutesResponse> => {
        return routeService.callWithBody<ShowRoutesResponse>('ShowRoutes', request, signal);
    },
    insertRoute: (request: InsertRouteRequest, signal?: AbortSignal): Promise<InsertRouteResponse> => {
        return routeService.callWithBody<InsertRouteResponse>('InsertRoute', request, signal);
    },
    deleteRoute: (request: DeleteRouteRequest, signal?: AbortSignal): Promise<DeleteRouteResponse> => {
        return routeService.callWithBody<DeleteRouteResponse>('DeleteRoute', request, signal);
    },
    flushRoutes: (request: FlushRoutesRequest, signal?: AbortSignal): Promise<FlushRoutesResponse> => {
        return routeService.callWithBody<FlushRoutesResponse>('FlushRoutes', request, signal);
    },
};
