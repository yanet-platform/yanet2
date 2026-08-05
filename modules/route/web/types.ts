/** A single flattened row in the FIB table: one (range, nexthop) pair. */
export interface FIBRowItem {
    /** Stable local ID — not sent to the server. */
    id: string;
    from: string;
    to: string;
    dst_mac: string;
    src_mac: string;
    device: string;
    /** Explicit nexthop counter name. Empty means "let the server generate it". */
    counter: string;
}

/** Row status relative to the last-known server snapshot. */
export type FIBRowStatus = 'same' | 'added' | 'changed';

/** Validation errors for a single row. null = valid, string = error message. */
export interface FIBRowErrors {
    /** Applies to both the From and To columns, sourced from one drawer field. */
    range: string | null;
    dst_mac: string | null;
    src_mac: string | null;
    device: string | null;
    counter: string | null;
}
