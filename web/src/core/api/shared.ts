// Shared filter/config wire types used by multiple module api files.

export interface Device {
    name?: string;
}

export interface VlanRange {
    from?: number;
    to?: number;
}

export interface IPNet {
    addr?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
    mask?: string | Uint8Array | number[]; // Base64 encoded bytes or raw bytes
}

export interface ListConfigsResponse {
    configs?: string[];
}
