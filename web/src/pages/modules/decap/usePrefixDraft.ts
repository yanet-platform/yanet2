import { useCallback } from 'react';
import { API } from '../../../api';
import type { PrefixRowItem } from './types';
import { prefixDraftReducer, initialPrefixDraftState } from './prefixDraftReducer';
import { useDraft } from '../../_shared/draft';
import type { UseDraftResult } from '../../_shared/draft';

export type UsePrefixDraftResult = UseDraftResult<PrefixRowItem>;

/**
 * Wraps decap prefix config data with a local-draft layer.
 *
 * Server state is fetched once on mount. All UI mutations go through
 * dispatchDraft and update only local state until the user explicitly calls
 * commitConfig. On commit the diff between draft and server is computed and
 * the minimal add/remove API calls are made.
 */
export const usePrefixDraft = (): UsePrefixDraftResult => {
    const load = useCallback(async (): Promise<Array<{ name: string; rows: PrefixRowItem[] }>> => {
        const inspectResp = await API.inspect.inspect();
        const cpConfigs = inspectResp.instance_info?.cp_configs ?? [];
        const configNames = cpConfigs
            .filter((c) => c.type === 'decap')
            .map((c) => c.name ?? '')
            .filter(Boolean);
        return Promise.all(
            configNames.map(async (name): Promise<{ name: string; rows: PrefixRowItem[] }> => {
                try {
                    const resp = await API.decap.showConfig({ name });
                    const rows: PrefixRowItem[] = (resp.prefixes ?? []).map((p) => ({ id: p, prefix: p }));
                    return { name, rows };
                } catch {
                    return { name, rows: [] };
                }
            }),
        );
    }, []);

    const commit = useCallback(async (
        configName: string,
        draftRows: PrefixRowItem[],
        serverRowsArg: PrefixRowItem[],
    ): Promise<void> => {
        const draftPrefixes = new Set(draftRows.map((r) => r.prefix));
        const serverPrefixes = new Set(serverRowsArg.map((r) => r.prefix));
        const added = [...draftPrefixes].filter((p) => !serverPrefixes.has(p));
        const removed = [...serverPrefixes].filter((p) => !draftPrefixes.has(p));
        if (added.length > 0) {
            await API.decap.addPrefixes({ name: configName, prefixes: added });
        }
        if (removed.length > 0) {
            await API.decap.removePrefixes({ name: configName, prefixes: removed });
        }
    }, []);

    return useDraft<PrefixRowItem>({
        load,
        commit,
        reducer: prefixDraftReducer,
        initialState: initialPrefixDraftState,
        toastSubject: 'prefix',
        errorSubject: 'Decap',
    });
};
