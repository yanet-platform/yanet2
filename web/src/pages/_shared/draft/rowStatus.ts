import type { RowStatus } from '../../../components';

/** Per-row draft status plus the removed-rows list, against a server snapshot. */
export interface RowStatusResult<T> {
    statusById: Map<string, RowStatus>;
    removedRows: T[];
}

/**
 * Classify each draft row against the server snapshot.
 *
 * A row present locally but not on the server is 'added'; one present in both
 * is 'same' or 'changed' per the caller's equality check; a server row absent
 * locally is collected into removedRows.
 */
export const computeRowStatuses = <T extends { id: string }>(
    rawRows: T[],
    rawServerRows: T[],
    isEqual: (server: T, local: T) => boolean,
): RowStatusResult<T> => {
    const statusById = new Map<string, RowStatus>();
    const serverById = new Map(rawServerRows.map((r) => [r.id, r]));
    for (const r of rawRows) {
        const s = serverById.get(r.id);
        if (!s) {
            statusById.set(r.id, 'added');
        } else {
            statusById.set(r.id, isEqual(s, r) ? 'same' : 'changed');
        }
    }
    const localIds = new Set(rawRows.map((r) => r.id));
    const removedRows = rawServerRows.filter((r) => !localIds.has(r.id));
    return { statusById, removedRows };
};
