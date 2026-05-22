import { useCallback, useEffect, useReducer, useState } from 'react';
import { API } from '../../../api';
import { toaster } from '../../../utils';
import type { FIBEntry, FIBNexthop, FIBRangeEntry } from '../../../api/routes';
import { ipRangeToCIDRs } from '../../../utils/netip';
import type { FIBRowItem } from './types';
import { fibDraftReducer, initialFIBDraftState } from './fibDraftReducer';
import type { FIBDraftAction } from './fibDraftReducer';

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

const EMPTY_ROWS: FIBRowItem[] = [];

export interface UseFIBDraftResult {
    draftConfigs: string[];
    loading: boolean;
    draftRows: (configName: string) => FIBRowItem[];
    serverRows: (configName: string) => FIBRowItem[];
    isDirty: (configName: string) => boolean;
    anyDirty: boolean;
    dispatchDraft: (action: FIBDraftAction) => void;
    commitConfig: (configName: string) => Promise<void>;
    discardConfig: (configName: string) => void;
    newRowId: () => string;
}

/**
 * Wraps FIB config data with a local-draft layer.
 *
 * Server state is fetched once on mount via the route.showFIB and route.listConfigs APIs.
 * All UI mutations go through dispatchDraft and update only local state until the user
 * explicitly calls commitConfig. On commit the full draft rows are sent via API.route.updateFIB
 * and the local server snapshot is updated so dirty clears.
 */
export const useFIBDraft = (): UseFIBDraftResult => {
    const [state, rawDispatch] = useReducer(fibDraftReducer, initialFIBDraftState);
    const [loading, setLoading] = useState(true);

    const dispatchDraft = useCallback((action: FIBDraftAction): void => {
        rawDispatch(action);
    }, []);

    const load = useCallback(async (): Promise<void> => {
        setLoading(true);
        try {
            const configsResp = await API.route.listConfigs();
            const configNames = configsResp.configs ?? [];

            const configs = await Promise.all(
                configNames.map(async (name): Promise<{ name: string; rows: FIBRowItem[] }> => {
                    try {
                        const fibResp = await API.route.showFIB({ name });
                        return { name, rows: flattenFIBEntries(fibResp.entries ?? []) };
                    } catch {
                        return { name, rows: [] };
                    }
                }),
            );

            rawDispatch({ type: 'LOAD_ALL_CONFIGS', configs });
        } catch (err) {
            toaster.error('fib-draft-load', 'Failed to load FIB configurations', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const commitConfig = useCallback(async (configName: string): Promise<void> => {
        const rows = state.draft[configName] ?? [];
        const entries = rowsToFIBEntries(rows);
        try {
            await API.route.updateFIB({ module_name: configName, entries });
            rawDispatch({ type: 'MARK_COMMITTED', configName });
            toaster.success(`fib-commit-${configName}`, `FIB "${configName}" committed.`);
        } catch (err) {
            toaster.error(`fib-commit-err-${configName}`, `Failed to commit "${configName}"`, err);
            throw err;
        }
    }, [state.draft]);

    const discardConfig = useCallback((configName: string): void => {
        rawDispatch({ type: 'DISCARD_CONFIG', configName });
    }, []);

    const draftRowsFor = useCallback((configName: string): FIBRowItem[] =>
        state.draft[configName] ?? EMPTY_ROWS, [state.draft]);

    const serverRowsFor = useCallback((configName: string): FIBRowItem[] =>
        state.server[configName] ?? EMPTY_ROWS, [state.server]);

    const isDirty = useCallback((configName: string): boolean =>
        state.dirty.has(configName), [state.dirty]);

    const draftConfigs = [
        ...state.serverConfigs,
        ...state.localOnlyConfigs,
    ];

    const anyDirty = state.dirty.size > 0;

    return {
        draftConfigs,
        loading,
        draftRows: draftRowsFor,
        serverRows: serverRowsFor,
        isDirty,
        anyDirty,
        dispatchDraft,
        commitConfig,
        discardConfig,
        newRowId: newRowId,
    };
};
