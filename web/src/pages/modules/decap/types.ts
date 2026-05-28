/** A single row in the decap prefix table. */
export interface PrefixRowItem {
    /** Stable local ID — equals the prefix string for server-loaded rows. */
    id: string;
    prefix: string;
}

/** Row status relative to the last-known server snapshot. */
export type PrefixRowStatus = 'same' | 'added' | 'changed';

/** Validation errors for a single row. null = valid, string = error message. */
export interface PrefixRowErrors {
    prefix: string | null;
}
