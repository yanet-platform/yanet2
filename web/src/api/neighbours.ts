import { createService, type CallOptions } from './client';

// Neighbour types
export interface MACAddress {
    addr: string | number; // uint64 - serialized as string in JSON
}

export interface Neighbour {
    next_hop?: string;
    link_addr?: MACAddress;
    hardware_addr?: MACAddress;
    state?: number; // NeighbourState enum
    updated_at?: string | number; // int64, UNIX timestamp in seconds - serialized as string in JSON
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
