import { useCallback } from 'react';
import { API, inventoryConfigNames, loadKnownConfigs, unionConfigNames } from '@yanet/core/api';
import { normalizeMAC, warnConfigsUnknown } from '@yanet/core/utils';
import type { FIBEntry, FIBNexthop } from '@yanet/core/api/routes';
import { ipRangeSpan, normalizeIPRange } from '@yanet/core/utils/netip';
import type { IPRangeWire } from '@yanet/core/utils/netip';
import type { FIBRowItem } from './types';
import { fibDraftReducer, initialFIBDraftState } from './fibDraftReducer';
import { useDraft } from '@yanet/core/components/draft';
import type { UseDraftResult } from '@yanet/core/components/draft';

let rowIdCounter = 0;
const newRowId = (): string => `row-${++rowIdCounter}-${Date.now()}`;

/** Flatten FIBEntry array into flat (range, nexthop) row items, one row per nexthop. */
export const flattenFIBEntries = (entries: FIBEntry[]): FIBRowItem[] => {
    const rows: FIBRowItem[] = [];
    for (const entry of entries) {
        const from = entry.range?.start ?? '';
        const to = entry.range?.end ?? '';
        const nexthops = entry.nexthops || [];
        if (nexthops.length === 0) {
            rows.push({ id: newRowId(), from, to, dst_mac: '', src_mac: '', device: '', counter: '' });
        } else {
            for (const nh of nexthops) {
                rows.push({
                    id: newRowId(),
                    from,
                    to,
                    dst_mac: nh.dst_mac || '',
                    src_mac: nh.src_mac || '',
                    device: nh.device || '',
                    counter: nh.counter || '',
                });
            }
        }
    }
    return rows;
};

/** Span of an entry whose range was already validated by rowsToFIBEntries. */
const requireSpan = (entry: FIBEntry): bigint => {
    const span = ipRangeSpan(entry.range);
    if (span === undefined) {
        throw new Error(`Cannot compute span for range "${entry.range?.start} - ${entry.range?.end}"`);
    }
    return span;
};

/**
 * Order entries so that lpm_insert's last-write-wins overlap resolution
 * reproduces longest-prefix-match: broadest ranges are inserted first,
 * narrowest last, so a more specific row always overrides a broader one it
 * overlaps. The sort is stable, so non-overlapping entries of equal span
 * keep their original relative order.
 */
const orderEntriesForCommit = (entries: FIBEntry[]): FIBEntry[] =>
    [...entries].sort((a, b) => {
        const spanA = requireSpan(a);
        const spanB = requireSpan(b);
        if (spanA === spanB) return 0;
        return spanA > spanB ? -1 : 1;
    });

/**
 * Group flat rows back into a FIBEntry list (rows sharing the same range,
 * wherever they sit in the table, collapse into one entry with several ECMP
 * nexthops — not just adjacent rows, since the table lets equal ranges land
 * apart).
 *
 * A row can reach here without having passed validateRow at all, via YAML
 * import. Throwing here rather than dropping the row is deliberate: a
 * dropped row would silently delete a route from the FIB, which is worse
 * than failing the whole commit loudly.
 */
export const rowsToFIBEntries = (rows: FIBRowItem[]): FIBEntry[] => {
    const byRange = new Map<string, FIBEntry>();
    const entries: FIBEntry[] = [];
    for (const row of rows) {
        const range: IPRangeWire | undefined = normalizeIPRange(row.from, row.to);
        if (!range) {
            throw new Error(`Cannot convert FIB row range "${row.from} - ${row.to}" to an IP range`);
        }

        const nh: FIBNexthop = {
            dst_mac: normalizeMAC(row.dst_mac) ?? row.dst_mac,
            src_mac: normalizeMAC(row.src_mac) ?? row.src_mac,
            device: row.device,
            counter: row.counter,
        };
        const key = `${range.start}-${range.end}`;
        const existing = byRange.get(key);
        if (existing) {
            existing.nexthops = [...(existing.nexthops || []), nh];
        } else {
            const entry: FIBEntry = { range, nexthops: [nh] };
            byRange.set(key, entry);
            entries.push(entry);
        }
    }
    return orderEntriesForCommit(entries);
};

export type UseFIBDraftResult = UseDraftResult<FIBRowItem>;

const loadConfig = async (name: string): Promise<{ name: string; rows: FIBRowItem[] }> => {
    const fibResp = await API.route.showFIB({ name });
    return { name, rows: flattenFIBEntries(fibResp.entries ?? []) };
};

/**
 * Wraps FIB config data with a local-draft layer.
 *
 * Server state is fetched once on mount via the route.listConfigs, inspect and route.showFIB APIs.
 * All UI mutations go through dispatchDraft and update only local state until the user
 * explicitly calls commitConfig. On commit the full draft rows are sent via API.route.updateFIB.
 * Since the server may materialize an empty counter into a generated name, the config is then
 * re-fetched so the draft and server snapshots reflect what was actually saved.
 */
export const useFIBDraft = (): UseFIBDraftResult => {
    const load = useCallback(async (): Promise<Array<{ name: string; rows: FIBRowItem[] }>> => {
        const [configsResp, inventoryNames] = await Promise.all([
            API.route.listConfigs(),
            inventoryConfigNames('route'),
        ]);
        const configNames = unionConfigNames(configsResp.configs ?? [], inventoryNames);
        return loadKnownConfigs(configNames, loadConfig, { onDropped: warnConfigsUnknown('route-configs-unknown', 'route') });
    }, []);

    const commit = useCallback(async (configName: string, draftRows: FIBRowItem[]): Promise<void> => {
        const entries = rowsToFIBEntries(draftRows);
        await API.route.updateFIB({ module_name: configName, entries });
    }, []);

    const draft = useDraft<FIBRowItem>({
        load,
        commit,
        reducer: fibDraftReducer,
        initialState: initialFIBDraftState,
        toastSubject: 'fib',
        errorSubject: 'FIB',
        cacheKey: 'route',
    });

    const commitConfig = useCallback(async (configName: string): Promise<void> => {
        await draft.commitConfig(configName);
        try {
            const { rows } = await loadConfig(configName);
            draft.dispatchDraft({ type: 'REFRESH_CONFIG', configName, rows });
        } catch {
            // Best effort: the draft already reflects the save, just not any
            // server-generated counter names until the next full reload.
        }
    }, [draft]);

    return { ...draft, commitConfig };
};
