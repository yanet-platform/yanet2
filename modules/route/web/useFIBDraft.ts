import { useCallback } from 'react';
import { API, inventoryConfigNames, loadKnownConfigs, unionConfigNames } from '@yanet/core/api';
import { warnConfigsUnknown } from '@yanet/core/utils';
import type { FIBEntry, FIBNexthop } from '@yanet/core/api/routes';
import { cidrToIPRange, ipRangeToCIDRs } from '@yanet/core/utils/netip';
import type { FIBRowItem } from './types';
import { fibDraftReducer, initialFIBDraftState } from './fibDraftReducer';
import { useDraft } from '@yanet/core/components/draft';
import type { UseDraftResult } from '@yanet/core/components/draft';

let rowIdCounter = 0;
const newRowId = (): string => `row-${++rowIdCounter}-${Date.now()}`;

/** Flatten FIBEntry array into flat (prefix, nexthop) row items. */
export const flattenFIBEntries = (entries: FIBEntry[]): FIBRowItem[] => {
    const rows: FIBRowItem[] = [];
    for (const entry of entries) {
        const cidrs = ipRangeToCIDRs(entry.range);
        const nexthops = entry.nexthops || [];
        for (const prefix of cidrs) {
            if (nexthops.length === 0) {
                rows.push({ id: newRowId(), prefix, dst_mac: '', src_mac: '', device: '' });
            } else {
                for (const nh of nexthops) {
                    rows.push({
                        id: newRowId(),
                        prefix,
                        dst_mac: nh.dst_mac?.addr || '',
                        src_mac: nh.src_mac?.addr || '',
                        device: nh.device || '',
                    });
                }
            }
        }
    }
    return rows;
};

/**
 * Group flat rows back into a FIBEntry list (consecutive rows with the
 * same prefix collapse into one entry with several ECMP nexthops).
 *
 * A row can reach here without having passed validateRow at all, and even a
 * row that passed it can still fail conversion, since validateRow's
 * isValidCidrPrefix is a looser check than cidrToIPRange's strict parser.
 * Throwing here rather than dropping the row is deliberate: a dropped row
 * would silently delete a route from the FIB, which is worse than failing
 * the whole commit loudly.
 */
export const rowsToFIBEntries = (rows: FIBRowItem[]): FIBEntry[] => {
    const entries: FIBEntry[] = [];
    for (const row of rows) {
        const range = cidrToIPRange(row.prefix);
        if (!range) {
            throw new Error(`Cannot convert FIB row prefix "${row.prefix}" to an IP range`);
        }

        const last = entries[entries.length - 1];
        const nh: FIBNexthop = {
            dst_mac: { addr: row.dst_mac },
            src_mac: { addr: row.src_mac },
            device: row.device,
        };
        if (last && last.range?.start === range.start && last.range?.end === range.end) {
            last.nexthops = [...(last.nexthops || []), nh];
        } else {
            entries.push({ range, nexthops: [nh] });
        }
    }
    return entries;
};

export type UseFIBDraftResult = UseDraftResult<FIBRowItem>;

/**
 * Wraps FIB config data with a local-draft layer.
 *
 * Server state is fetched once on mount via the route.listConfigs, inspect and route.showFIB APIs.
 * All UI mutations go through dispatchDraft and update only local state until the user
 * explicitly calls commitConfig. On commit the full draft rows are sent via API.route.updateFIB
 * and the local server snapshot is updated so dirty clears.
 */
export const useFIBDraft = (): UseFIBDraftResult => {
    const load = useCallback(async (): Promise<Array<{ name: string; rows: FIBRowItem[] }>> => {
        const [configsResp, inventoryNames] = await Promise.all([
            API.route.listConfigs(),
            inventoryConfigNames('route'),
        ]);
        const configNames = unionConfigNames(configsResp.configs ?? [], inventoryNames);
        return loadKnownConfigs(configNames, async (name): Promise<{ name: string; rows: FIBRowItem[] }> => {
            const fibResp = await API.route.showFIB({ name });
            return { name, rows: flattenFIBEntries(fibResp.entries ?? []) };
        }, { onDropped: warnConfigsUnknown('route-configs-unknown', 'route') });
    }, []);

    const commit = useCallback(async (configName: string, draftRows: FIBRowItem[]): Promise<void> => {
        const entries = rowsToFIBEntries(draftRows);
        await API.route.updateFIB({ module_name: configName, entries });
    }, []);

    return useDraft<FIBRowItem>({
        load,
        commit,
        reducer: fibDraftReducer,
        initialState: initialFIBDraftState,
        toastSubject: 'fib',
        errorSubject: 'FIB',
        cacheKey: 'route',
    });
};
