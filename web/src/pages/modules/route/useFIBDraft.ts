import { useCallback } from 'react';
import { API } from '../../../api';
import type { FIBEntry, FIBNexthop, FIBRangeEntry } from '../../../api/routes';
import { ipRangeToCIDRs } from '../../../utils/netip';
import type { FIBRowItem } from './types';
import { fibDraftReducer, initialFIBDraftState } from './fibDraftReducer';
import { useDraft } from '../../_shared/draft';
import type { UseDraftResult } from '../../_shared/draft';

let rowIdCounter = 0;
const newRowId = (): string => `row-${++rowIdCounter}-${Date.now()}`;

/** Flatten FIBRangeEntry array into flat (prefix, nexthop) row items. */
export const flattenFIBEntries = (entries: FIBRangeEntry[]): FIBRowItem[] => {
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

/** Group flat rows back into FIBEntry list (consecutive rows with same prefix → one entry). */
export const rowsToFIBEntries = (rows: FIBRowItem[]): FIBEntry[] => {
    const entries: FIBEntry[] = [];
    for (const row of rows) {
        const last = entries[entries.length - 1];
        const nh: FIBNexthop = {
            dst_mac: { addr: row.dst_mac },
            src_mac: { addr: row.src_mac },
            device: row.device,
        };
        if (last && last.prefix === row.prefix) {
            last.nexthops = [...(last.nexthops || []), nh];
        } else {
            entries.push({ prefix: row.prefix, nexthops: [nh] });
        }
    }
    return entries;
};

export type UseFIBDraftResult = UseDraftResult<FIBRowItem>;

/**
 * Wraps FIB config data with a local-draft layer.
 *
 * Server state is fetched once on mount via the route.showFIB and route.listConfigs APIs.
 * All UI mutations go through dispatchDraft and update only local state until the user
 * explicitly calls commitConfig. On commit the full draft rows are sent via API.route.updateFIB
 * and the local server snapshot is updated so dirty clears.
 */
export const useFIBDraft = (): UseFIBDraftResult => {
    const load = useCallback(async (): Promise<Array<{ name: string; rows: FIBRowItem[] }>> => {
        const configsResp = await API.route.listConfigs();
        const configNames = configsResp.configs ?? [];
        return Promise.all(
            configNames.map(async (name): Promise<{ name: string; rows: FIBRowItem[] }> => {
                try {
                    const fibResp = await API.route.showFIB({ name });
                    return { name, rows: flattenFIBEntries(fibResp.entries ?? []) };
                } catch {
                    return { name, rows: [] };
                }
            }),
        );
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
    });
};
