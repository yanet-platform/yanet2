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
    updated_at?: string | number; // int64, UNIX timestamp in seconds
    source?: string;
    priority?: number;
    device?: string;
}

export interface ListNeighboursResponse {
    neighbours?: Neighbour[];
}

export interface NeighbourTableInfo {
    name?: string;
    default_priority?: number;
    entry_count?: string | number; // int64
    built_in?: boolean;
}

export interface ListNeighbourTablesResponse {
    tables?: NeighbourTableInfo[];
}

const neighbourService = createService('routepb.Neighbour');

export const neighbours = {
    list: (table?: string, options?: CallOptions): Promise<ListNeighboursResponse> => {
        if (table) {
            return neighbourService.callWithBody<ListNeighboursResponse>('List', { table }, options);
        }
        return neighbourService.call<ListNeighboursResponse>('List', options);
    },

    listTables: (options?: CallOptions): Promise<ListNeighbourTablesResponse> => {
        return neighbourService.call<ListNeighbourTablesResponse>('ListTables', options);
    },

    updateNeighbours: (table: string, entries: Neighbour[], options?: CallOptions): Promise<void> => {
        return neighbourService.callWithBody<void>('UpdateNeighbours', { table, entries }, options);
    },

    removeNeighbours: (table: string, nextHops: string[], options?: CallOptions): Promise<void> => {
        return neighbourService.callWithBody<void>('RemoveNeighbours', { table, next_hops: nextHops }, options);
    },

    createTable: (name: string, defaultPriority: number, options?: CallOptions): Promise<void> => {
        return neighbourService.callWithBody<void>('CreateTable', { name, default_priority: defaultPriority }, options);
    },

    updateTable: (name: string, defaultPriority: number, options?: CallOptions): Promise<void> => {
        return neighbourService.callWithBody<void>('UpdateTable', { name, default_priority: defaultPriority }, options);
    },

    removeTable: (name: string, options?: CallOptions): Promise<void> => {
        return neighbourService.callWithBody<void>('RemoveTable', { name }, options);
    },
};
