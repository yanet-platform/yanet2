/** A single flattened row in the FIB table: one (prefix, nexthop) pair. */
export interface FIBRowItem {
    /** Stable local ID — not sent to the server. */
    id: string;
    prefix: string;
    dst_mac: string;
    src_mac: string;
    device: string;
}

/** Row status relative to the last-known server snapshot. */
export type FIBRowStatus = 'same' | 'added' | 'changed';

/** Validation errors for a single row. null = valid, string = error message. */
export interface FIBRowErrors {
    prefix: string | null;
    dst_mac: string | null;
    src_mac: string | null;
    device: string | null;
}
