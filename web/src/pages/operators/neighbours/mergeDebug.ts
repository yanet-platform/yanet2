import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { ipAddressToString } from '../../../utils/netip';

const ZERO_MAC = '00:00:00:00:00:00';

/** A lower-priority entry that was shadowed by the merged winner. */
export interface ShadowedCandidate {
    /** Name of the table that holds this entry. */
    table: string;
    /** The shadowed neighbour entry. */
    entry: Neighbour;
    /** Priority of this candidate's table. */
    priority: number;
    /** Whether this candidate's neighbour MAC differs from the winner's (true = conflict). */
    macDiffers: boolean;
}

/** Result of merge-debug analysis for a single merged row. */
export interface MergeDebugResult {
    /** All shadowed candidates, sorted by priority ascending (lower value = higher precedence / wins). */
    shadowed: ShadowedCandidate[];
    /** True when at least one shadowed candidate has a different non-zero neighbour MAC. */
    macConflict: boolean;
}

/**
 * Derives shadowed candidates and MAC-conflict info for a merged-view row.
 *
 * Matches candidate entries by next_hop alone — the backend merge map is keyed
 * by next_hop only, so device is not part of the match key. Results are sorted
 * by priority ascending (lower value = higher precedence / wins).
 * Only called when isMergedView is true.
 */
export const getMergeDebug = (
    winner: Neighbour,
    cache: Map<string, Neighbour[]>,
    tables: NeighbourTableInfo[],
): MergeDebugResult => {
    const winnerIp = ipAddressToString(winner.next_hop);
    const winnerMac = winner.link_addr?.addr || '';

    const candidates: ShadowedCandidate[] = [];

    for (const tableInfo of tables) {
        const tableName = tableInfo.name || '';
        if (!tableName) continue;
        if (tableName === winner.source) continue;

        const entries = cache.get(tableName) || [];
        for (const entry of entries) {
            const entryIp = ipAddressToString(entry.next_hop);
            if (entryIp !== winnerIp) continue;

            const entryMac = entry.link_addr?.addr || '';
            const macDiffers =
                entryMac !== winnerMac &&
                entryMac !== ZERO_MAC &&
                winnerMac !== ZERO_MAC;
            candidates.push({
                table: tableName,
                entry,
                priority: entry.priority ?? tableInfo.default_priority ?? 0,
                macDiffers,
            });
        }
    }

    // Sort ascending: lower priority value = higher precedence, listed first.
    candidates.sort((a, b) => a.priority - b.priority);

    const macConflict = candidates.some((c) => c.macDiffers);

    return { shadowed: candidates, macConflict };
};

