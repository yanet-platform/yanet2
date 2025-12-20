import { createService, type CallOptions } from './client';

// Neighbour types
export interface MACAddress {
    addr: string | number; // uint64 - serialized as string in JSON
}

export interface Neighbour {
    nextHop?: string; // next_hop
    linkAddr?: MACAddress; // link_addr
    hardwareAddr?: MACAddress; // hardware_addr
    state?: number; // NeighbourState enum
    updatedAt?: string | number; // updated_at (int64, UNIX timestamp in seconds) - serialized as string in JSON
}

export interface ListNeighboursResponse {
    neighbours?: Neighbour[];
}

const neighbourService = createService('routepb.Neighbour');

export const neighbours = {
    list: (options?: CallOptions): Promise<ListNeighboursResponse> => {
        return neighbourService.call<ListNeighboursResponse>('List', options);
    },
};
