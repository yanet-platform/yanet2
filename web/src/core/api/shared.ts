// Shared filter/config wire types used by multiple module api files.

export interface Device {
    name?: string;
}

export interface VlanRange {
    from?: number;
    to?: number;
}

export interface ListConfigsResponse {
    configs?: string[];
}
